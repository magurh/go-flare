// Copyright (C) 2019-2024, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package platformvm

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/ava-labs/avalanchego/chains"
	"github.com/ava-labs/avalanchego/chains/atomic"
	"github.com/ava-labs/avalanchego/database"
	"github.com/ava-labs/avalanchego/database/memdb"
	"github.com/ava-labs/avalanchego/database/prefixdb"
	"github.com/ava-labs/avalanchego/ids"
	"github.com/ava-labs/avalanchego/network/p2p"
	"github.com/ava-labs/avalanchego/network/p2p/gossip"
	"github.com/ava-labs/avalanchego/snow/choices"
	"github.com/ava-labs/avalanchego/snow/consensus/snowman"
	"github.com/ava-labs/avalanchego/snow/engine/common"
	"github.com/ava-labs/avalanchego/snow/snowtest"
	"github.com/ava-labs/avalanchego/snow/uptime"
	"github.com/ava-labs/avalanchego/snow/validators"
	"github.com/ava-labs/avalanchego/utils/bloom"
	"github.com/ava-labs/avalanchego/utils/constants"
	"github.com/ava-labs/avalanchego/utils/crypto/bls"
	"github.com/ava-labs/avalanchego/utils/crypto/secp256k1"
	"github.com/ava-labs/avalanchego/utils/timer/mockable"
	"github.com/ava-labs/avalanchego/version"
	"github.com/ava-labs/avalanchego/vms/components/avax"
	"github.com/ava-labs/avalanchego/vms/platformvm/block"
	"github.com/ava-labs/avalanchego/vms/platformvm/config"
	"github.com/ava-labs/avalanchego/vms/platformvm/metrics"
	"github.com/ava-labs/avalanchego/vms/platformvm/network"
	"github.com/ava-labs/avalanchego/vms/platformvm/reward"
	"github.com/ava-labs/avalanchego/vms/platformvm/signer"
	"github.com/ava-labs/avalanchego/vms/platformvm/state"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs"
	"github.com/ava-labs/avalanchego/vms/platformvm/txs/executor"
	"github.com/ava-labs/avalanchego/vms/secp256k1fx"

	blockexecutor "github.com/ava-labs/avalanchego/vms/platformvm/block/executor"
)

func TestAddDelegatorTxOverDelegatedRegression(t *testing.T) {
	require := require.New(t)
	vm, _, _ := defaultVM(t, cortina)
	vm.ctx.Lock.Lock()
	defer vm.ctx.Lock.Unlock()

	validatorStartTime := vm.clock.Time().Add(executor.SyncBound).Add(1 * time.Second)
	validatorEndTime := validatorStartTime.Add(360 * 24 * time.Hour)

	nodeID := ids.GenerateTestNodeID()
	changeAddr := keys[0].PublicKey().Address()

	// create valid tx
	addValidatorTx, err := vm.txBuilder.NewAddValidatorTx(
		vm.MinValidatorStake,
		uint64(validatorStartTime.Unix()),
		uint64(validatorEndTime.Unix()),
		nodeID,
		changeAddr,
		reward.PercentDenominator,
		[]*secp256k1.PrivateKey{keys[0]},
		changeAddr,
		nil,
	)
	require.NoError(err)

	// trigger block creation
	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), addValidatorTx))
	vm.ctx.Lock.Lock()

	addValidatorBlock, err := vm.Builder.BuildBlock(context.Background())
	require.NoError(err)
	require.NoError(addValidatorBlock.Verify(context.Background()))
	require.NoError(addValidatorBlock.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	vm.clock.Set(validatorStartTime)

	firstAdvanceTimeBlock, err := vm.Builder.BuildBlock(context.Background())
	require.NoError(err)
	require.NoError(firstAdvanceTimeBlock.Verify(context.Background()))
	require.NoError(firstAdvanceTimeBlock.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	firstDelegatorStartTime := validatorStartTime.Add(executor.SyncBound).Add(1 * time.Second)
	firstDelegatorEndTime := firstDelegatorStartTime.Add(vm.MinStakeDuration)

	// create valid tx
	addFirstDelegatorTx, err := vm.txBuilder.NewAddDelegatorTx(
		4*vm.MinValidatorStake, // maximum amount of stake this delegator can provide
		uint64(firstDelegatorStartTime.Unix()),
		uint64(firstDelegatorEndTime.Unix()),
		nodeID,
		changeAddr,
		[]*secp256k1.PrivateKey{keys[0], keys[1]},
		changeAddr,
		nil,
	)
	require.NoError(err)

	// trigger block creation
	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), addFirstDelegatorTx))
	vm.ctx.Lock.Lock()

	addFirstDelegatorBlock, err := vm.Builder.BuildBlock(context.Background())
	require.NoError(err)
	require.NoError(addFirstDelegatorBlock.Verify(context.Background()))
	require.NoError(addFirstDelegatorBlock.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	vm.clock.Set(firstDelegatorStartTime)

	secondAdvanceTimeBlock, err := vm.Builder.BuildBlock(context.Background())
	require.NoError(err)
	require.NoError(secondAdvanceTimeBlock.Verify(context.Background()))
	require.NoError(secondAdvanceTimeBlock.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	secondDelegatorStartTime := firstDelegatorEndTime.Add(2 * time.Second)
	secondDelegatorEndTime := secondDelegatorStartTime.Add(vm.MinStakeDuration)

	vm.clock.Set(secondDelegatorStartTime.Add(-10 * executor.SyncBound))

	// create valid tx
	addSecondDelegatorTx, err := vm.txBuilder.NewAddDelegatorTx(
		vm.MinDelegatorStake,
		uint64(secondDelegatorStartTime.Unix()),
		uint64(secondDelegatorEndTime.Unix()),
		nodeID,
		changeAddr,
		[]*secp256k1.PrivateKey{keys[0], keys[1], keys[3]},
		changeAddr,
		nil,
	)
	require.NoError(err)

	// trigger block creation
	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), addSecondDelegatorTx))
	vm.ctx.Lock.Lock()

	addSecondDelegatorBlock, err := vm.Builder.BuildBlock(context.Background())
	require.NoError(err)
	require.NoError(addSecondDelegatorBlock.Verify(context.Background()))
	require.NoError(addSecondDelegatorBlock.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	thirdDelegatorStartTime := firstDelegatorEndTime.Add(-time.Second)
	thirdDelegatorEndTime := thirdDelegatorStartTime.Add(vm.MinStakeDuration)

	// create valid tx
	addThirdDelegatorTx, err := vm.txBuilder.NewAddDelegatorTx(
		vm.MinDelegatorStake,
		uint64(thirdDelegatorStartTime.Unix()),
		uint64(thirdDelegatorEndTime.Unix()),
		nodeID,
		changeAddr,
		[]*secp256k1.PrivateKey{keys[0], keys[1], keys[4]},
		changeAddr,
		nil,
	)
	require.NoError(err)

	// trigger block creation
	vm.ctx.Lock.Unlock()
	err = vm.issueTx(context.Background(), addThirdDelegatorTx)
	require.ErrorIs(err, executor.ErrOverDelegated)
	vm.ctx.Lock.Lock()
}

func TestAddDelegatorTxHeapCorruption(t *testing.T) {
	validatorStartTime := latestForkTime.Add(executor.SyncBound).Add(1 * time.Second)
	validatorEndTime := validatorStartTime.Add(360 * 24 * time.Hour)
	validatorStake := defaultMaxValidatorStake / 5

	delegator1StartTime := validatorStartTime
	delegator1EndTime := delegator1StartTime.Add(3 * defaultMinStakingDuration)
	delegator1Stake := defaultMinValidatorStake

	delegator2StartTime := validatorStartTime.Add(1 * defaultMinStakingDuration)
	delegator2EndTime := delegator1StartTime.Add(6 * defaultMinStakingDuration)
	delegator2Stake := defaultMinValidatorStake

	delegator3StartTime := validatorStartTime.Add(2 * defaultMinStakingDuration)
	delegator3EndTime := delegator1StartTime.Add(4 * defaultMinStakingDuration)
	delegator3Stake := defaultMaxValidatorStake - validatorStake - 2*defaultMinValidatorStake

	delegator4StartTime := validatorStartTime.Add(5 * defaultMinStakingDuration)
	delegator4EndTime := delegator1StartTime.Add(7 * defaultMinStakingDuration)
	delegator4Stake := defaultMaxValidatorStake - validatorStake - defaultMinValidatorStake

	tests := []struct {
		name    string
		ap3Time time.Time
	}{
		{
			name:    "pre-upgrade is no longer restrictive",
			ap3Time: validatorEndTime,
		},
		{
			name:    "post-upgrade calculate max stake correctly",
			ap3Time: defaultGenesisTime,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require := require.New(t)

			vm, _, _ := defaultVM(t, apricotPhase3)
			vm.ApricotPhase3Time = test.ap3Time

			vm.ctx.Lock.Lock()
			defer vm.ctx.Lock.Unlock()

			key, err := secp256k1.NewPrivateKey()
			require.NoError(err)

			id := key.PublicKey().Address()
			nodeID := ids.GenerateTestNodeID()
			changeAddr := keys[0].PublicKey().Address()

			// create valid tx
			addValidatorTx, err := vm.txBuilder.NewAddValidatorTx(
				validatorStake,
				uint64(validatorStartTime.Unix()),
				uint64(validatorEndTime.Unix()),
				nodeID,
				id,
				reward.PercentDenominator,
				[]*secp256k1.PrivateKey{keys[0], keys[1]},
				changeAddr,
				nil,
			)
			require.NoError(err)

			// issue the add validator tx
			vm.ctx.Lock.Unlock()
			require.NoError(vm.issueTx(context.Background(), addValidatorTx))
			vm.ctx.Lock.Lock()

			// trigger block creation for the validator tx
			addValidatorBlock, err := vm.Builder.BuildBlock(context.Background())
			require.NoError(err)
			require.NoError(addValidatorBlock.Verify(context.Background()))
			require.NoError(addValidatorBlock.Accept(context.Background()))
			require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

			// create valid tx
			addFirstDelegatorTx, err := vm.txBuilder.NewAddDelegatorTx(
				delegator1Stake,
				uint64(delegator1StartTime.Unix()),
				uint64(delegator1EndTime.Unix()),
				nodeID,
				keys[0].PublicKey().Address(),
				[]*secp256k1.PrivateKey{keys[0], keys[1]},
				changeAddr,
				nil,
			)
			require.NoError(err)

			// issue the first add delegator tx
			vm.ctx.Lock.Unlock()
			require.NoError(vm.issueTx(context.Background(), addFirstDelegatorTx))
			vm.ctx.Lock.Lock()

			// trigger block creation for the first add delegator tx
			addFirstDelegatorBlock, err := vm.Builder.BuildBlock(context.Background())
			require.NoError(err)
			require.NoError(addFirstDelegatorBlock.Verify(context.Background()))
			require.NoError(addFirstDelegatorBlock.Accept(context.Background()))
			require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

			// create valid tx
			addSecondDelegatorTx, err := vm.txBuilder.NewAddDelegatorTx(
				delegator2Stake,
				uint64(delegator2StartTime.Unix()),
				uint64(delegator2EndTime.Unix()),
				nodeID,
				keys[0].PublicKey().Address(),
				[]*secp256k1.PrivateKey{keys[0], keys[1]},
				changeAddr,
				nil,
			)
			require.NoError(err)

			// issue the second add delegator tx
			vm.ctx.Lock.Unlock()
			require.NoError(vm.issueTx(context.Background(), addSecondDelegatorTx))
			vm.ctx.Lock.Lock()

			// trigger block creation for the second add delegator tx
			addSecondDelegatorBlock, err := vm.Builder.BuildBlock(context.Background())
			require.NoError(err)
			require.NoError(addSecondDelegatorBlock.Verify(context.Background()))
			require.NoError(addSecondDelegatorBlock.Accept(context.Background()))
			require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

			// create valid tx
			addThirdDelegatorTx, err := vm.txBuilder.NewAddDelegatorTx(
				delegator3Stake,
				uint64(delegator3StartTime.Unix()),
				uint64(delegator3EndTime.Unix()),
				nodeID,
				keys[0].PublicKey().Address(),
				[]*secp256k1.PrivateKey{keys[0], keys[1]},
				changeAddr,
				nil,
			)
			require.NoError(err)

			// issue the third add delegator tx
			vm.ctx.Lock.Unlock()
			require.NoError(vm.issueTx(context.Background(), addThirdDelegatorTx))
			vm.ctx.Lock.Lock()

			// trigger block creation for the third add delegator tx
			addThirdDelegatorBlock, err := vm.Builder.BuildBlock(context.Background())
			require.NoError(err)
			require.NoError(addThirdDelegatorBlock.Verify(context.Background()))
			require.NoError(addThirdDelegatorBlock.Accept(context.Background()))
			require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

			// create valid tx
			addFourthDelegatorTx, err := vm.txBuilder.NewAddDelegatorTx(
				delegator4Stake,
				uint64(delegator4StartTime.Unix()),
				uint64(delegator4EndTime.Unix()),
				nodeID,
				keys[0].PublicKey().Address(),
				[]*secp256k1.PrivateKey{keys[0], keys[1]},
				changeAddr,
				nil,
			)
			require.NoError(err)

			// issue the fourth add delegator tx
			vm.ctx.Lock.Unlock()
			require.NoError(vm.issueTx(context.Background(), addFourthDelegatorTx))
			vm.ctx.Lock.Lock()

			// trigger block creation for the fourth add delegator tx
			addFourthDelegatorBlock, err := vm.Builder.BuildBlock(context.Background())
			require.NoError(err)
			require.NoError(addFourthDelegatorBlock.Verify(context.Background()))
			require.NoError(addFourthDelegatorBlock.Accept(context.Background()))
			require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))
		})
	}
}

// Test that calling Verify on a block with an unverified parent doesn't cause a
// panic.
func TestUnverifiedParentPanicRegression(t *testing.T) {
	require := require.New(t)

	baseDB := memdb.New()
	atomicDB := prefixdb.New([]byte{1}, baseDB)

	vm := &VM{Config: config.Config{
		Chains:                 chains.TestManager,
		Validators:             validators.NewManager(),
		UptimeLockedCalculator: uptime.NewLockedCalculator(),
		MinStakeDuration:       defaultMinStakingDuration,
		MaxStakeDuration:       defaultMaxStakingDuration,
		RewardConfig:           defaultRewardConfig,
		BanffTime:              latestForkTime,
		CortinaTime:            mockable.MaxTime,
		DurangoTime:            mockable.MaxTime,
	}}

	ctx := snowtest.Context(t, snowtest.PChainID)
	ctx.Lock.Lock()
	defer func() {
		require.NoError(vm.Shutdown(context.Background()))
		ctx.Lock.Unlock()
	}()

	_, genesisBytes := defaultGenesis(t, ctx.AVAXAssetID)

	msgChan := make(chan common.Message, 1)
	require.NoError(vm.Initialize(
		context.Background(),
		ctx,
		baseDB,
		genesisBytes,
		nil,
		nil,
		msgChan,
		nil,
		nil,
	))

	m := atomic.NewMemory(atomicDB)
	vm.ctx.SharedMemory = m.NewSharedMemory(ctx.ChainID)

	// set time to post Banff fork
	vm.clock.Set(latestForkTime.Add(time.Second))
	vm.state.SetTimestamp(latestForkTime.Add(time.Second))

	key0 := keys[0]
	key1 := keys[1]
	addr0 := key0.PublicKey().Address()
	addr1 := key1.PublicKey().Address()

	addSubnetTx0, err := vm.txBuilder.NewCreateSubnetTx(
		1,
		[]ids.ShortID{addr0},
		[]*secp256k1.PrivateKey{key0},
		addr0,
		nil,
	)
	require.NoError(err)

	addSubnetTx1, err := vm.txBuilder.NewCreateSubnetTx(
		1,
		[]ids.ShortID{addr1},
		[]*secp256k1.PrivateKey{key1},
		addr1,
		nil,
	)
	require.NoError(err)

	addSubnetTx2, err := vm.txBuilder.NewCreateSubnetTx(
		1,
		[]ids.ShortID{addr1},
		[]*secp256k1.PrivateKey{key1},
		addr0,
		nil,
	)
	require.NoError(err)

	preferredID := vm.manager.Preferred()
	preferred, err := vm.manager.GetBlock(preferredID)
	require.NoError(err)
	preferredChainTime := preferred.Timestamp()
	preferredHeight := preferred.Height()

	statelessStandardBlk, err := block.NewBanffStandardBlock(
		preferredChainTime,
		preferredID,
		preferredHeight+1,
		[]*txs.Tx{addSubnetTx0},
	)
	require.NoError(err)
	addSubnetBlk0 := vm.manager.NewBlock(statelessStandardBlk)

	statelessStandardBlk, err = block.NewBanffStandardBlock(
		preferredChainTime,
		preferredID,
		preferredHeight+1,
		[]*txs.Tx{addSubnetTx1},
	)
	require.NoError(err)
	addSubnetBlk1 := vm.manager.NewBlock(statelessStandardBlk)

	statelessStandardBlk, err = block.NewBanffStandardBlock(
		preferredChainTime,
		addSubnetBlk1.ID(),
		preferredHeight+2,
		[]*txs.Tx{addSubnetTx2},
	)
	require.NoError(err)
	addSubnetBlk2 := vm.manager.NewBlock(statelessStandardBlk)

	_, err = vm.ParseBlock(context.Background(), addSubnetBlk0.Bytes())
	require.NoError(err)

	_, err = vm.ParseBlock(context.Background(), addSubnetBlk1.Bytes())
	require.NoError(err)

	_, err = vm.ParseBlock(context.Background(), addSubnetBlk2.Bytes())
	require.NoError(err)

	require.NoError(addSubnetBlk0.Verify(context.Background()))
	require.NoError(addSubnetBlk0.Accept(context.Background()))

	// Doesn't matter what verify returns as long as it's not panicking.
	_ = addSubnetBlk2.Verify(context.Background())
}

func TestRejectedStateRegressionInvalidValidatorTimestamp(t *testing.T) {
	require := require.New(t)

	vm, baseDB, mutableSharedMemory := defaultVM(t, cortina)
	vm.ctx.Lock.Lock()
	defer vm.ctx.Lock.Unlock()

	nodeID := ids.GenerateTestNodeID()
	newValidatorStartTime := vm.clock.Time().Add(executor.SyncBound).Add(1 * time.Second)
	newValidatorEndTime := newValidatorStartTime.Add(defaultMinStakingDuration)

	// Create the tx to add a new validator
	addValidatorTx, err := vm.txBuilder.NewAddValidatorTx(
		vm.MinValidatorStake,
		uint64(newValidatorStartTime.Unix()),
		uint64(newValidatorEndTime.Unix()),
		nodeID,
		ids.GenerateTestShortID(),
		reward.PercentDenominator,
		[]*secp256k1.PrivateKey{keys[0]},
		ids.ShortEmpty,
		nil,
	)
	require.NoError(err)

	// Create the standard block to add the new validator
	preferredID := vm.manager.Preferred()
	preferred, err := vm.manager.GetBlock(preferredID)
	require.NoError(err)
	preferredChainTime := preferred.Timestamp()
	preferredHeight := preferred.Height()

	statelessBlk, err := block.NewBanffStandardBlock(
		preferredChainTime,
		preferredID,
		preferredHeight+1,
		[]*txs.Tx{addValidatorTx},
	)
	require.NoError(err)

	addValidatorStandardBlk := vm.manager.NewBlock(statelessBlk)
	require.NoError(addValidatorStandardBlk.Verify(context.Background()))

	// Verify that the new validator now in pending validator set
	{
		onAccept, found := vm.manager.GetState(addValidatorStandardBlk.ID())
		require.True(found)

		_, err := onAccept.GetPendingValidator(constants.PrimaryNetworkID, nodeID)
		require.NoError(err)
	}

	// Create the UTXO that will be added to shared memory
	utxo := &avax.UTXO{
		UTXOID: avax.UTXOID{
			TxID: ids.GenerateTestID(),
		},
		Asset: avax.Asset{
			ID: vm.ctx.AVAXAssetID,
		},
		Out: &secp256k1fx.TransferOutput{
			Amt:          vm.TxFee,
			OutputOwners: secp256k1fx.OutputOwners{},
		},
	}

	// Create the import tx that will fail verification
	unsignedImportTx := &txs.ImportTx{
		BaseTx: txs.BaseTx{BaseTx: avax.BaseTx{
			NetworkID:    vm.ctx.NetworkID,
			BlockchainID: vm.ctx.ChainID,
		}},
		SourceChain: vm.ctx.XChainID,
		ImportedInputs: []*avax.TransferableInput{
			{
				UTXOID: utxo.UTXOID,
				Asset:  utxo.Asset,
				In: &secp256k1fx.TransferInput{
					Amt: vm.TxFee,
				},
			},
		},
	}
	signedImportTx := &txs.Tx{Unsigned: unsignedImportTx}
	require.NoError(signedImportTx.Sign(txs.Codec, [][]*secp256k1.PrivateKey{
		{}, // There is one input, with no required signers
	}))

	// Create the standard block that will fail verification, and then be
	// re-verified.
	preferredChainTime = addValidatorStandardBlk.Timestamp()
	preferredID = addValidatorStandardBlk.ID()
	preferredHeight = addValidatorStandardBlk.Height()

	statelessImportBlk, err := block.NewBanffStandardBlock(
		preferredChainTime,
		preferredID,
		preferredHeight+1,
		[]*txs.Tx{signedImportTx},
	)
	require.NoError(err)

	importBlk := vm.manager.NewBlock(statelessImportBlk)

	// Because the shared memory UTXO hasn't been populated, this block is
	// currently invalid.
	err = importBlk.Verify(context.Background())
	require.ErrorIs(err, database.ErrNotFound)

	// Because we no longer ever reject a block in verification, the status
	// should remain as processing.
	importBlkStatus := importBlk.Status()
	require.Equal(choices.Processing, importBlkStatus)

	// Populate the shared memory UTXO.
	m := atomic.NewMemory(prefixdb.New([]byte{5}, baseDB))

	mutableSharedMemory.SharedMemory = m.NewSharedMemory(vm.ctx.ChainID)
	peerSharedMemory := m.NewSharedMemory(vm.ctx.XChainID)

	utxoBytes, err := txs.Codec.Marshal(txs.CodecVersion, utxo)
	require.NoError(err)

	inputID := utxo.InputID()
	require.NoError(peerSharedMemory.Apply(
		map[ids.ID]*atomic.Requests{
			vm.ctx.ChainID: {
				PutRequests: []*atomic.Element{
					{
						Key:   inputID[:],
						Value: utxoBytes,
					},
				},
			},
		},
	))

	// Because the shared memory UTXO has now been populated, the block should
	// pass verification.
	require.NoError(importBlk.Verify(context.Background()))

	// The status shouldn't have been changed during a successful verification.
	importBlkStatus = importBlk.Status()
	require.Equal(choices.Processing, importBlkStatus)

	// Move chain time ahead to bring the new validator from the pending
	// validator set into the current validator set.
	vm.clock.Set(newValidatorStartTime)

	// Create the proposal block that should have moved the new validator from
	// the pending validator set into the current validator set.
	preferredID = importBlk.ID()
	preferredHeight = importBlk.Height()

	statelessAdvanceTimeStandardBlk, err := block.NewBanffStandardBlock(
		newValidatorStartTime,
		preferredID,
		preferredHeight+1,
		nil,
	)
	require.NoError(err)

	advanceTimeStandardBlk := vm.manager.NewBlock(statelessAdvanceTimeStandardBlk)
	require.NoError(advanceTimeStandardBlk.Verify(context.Background()))

	// Accept all the blocks
	allBlocks := []snowman.Block{
		addValidatorStandardBlk,
		importBlk,
		advanceTimeStandardBlk,
	}
	for _, blk := range allBlocks {
		require.NoError(blk.Accept(context.Background()))

		status := blk.Status()
		require.Equal(choices.Accepted, status)
	}

	// Force a reload of the state from the database.
	vm.Config.Validators = validators.NewManager()
	execCfg, _ := config.GetExecutionConfig(nil)
	newState, err := state.New(
		vm.db,
		nil,
		prometheus.NewRegistry(),
		&vm.Config,
		execCfg,
		vm.ctx,
		metrics.Noop,
		reward.NewCalculator(vm.Config.RewardConfig),
	)
	require.NoError(err)

	// Verify that new validator is now in the current validator set.
	{
		_, err := newState.GetCurrentValidator(constants.PrimaryNetworkID, nodeID)
		require.NoError(err)

		_, err = newState.GetPendingValidator(constants.PrimaryNetworkID, nodeID)
		require.ErrorIs(err, database.ErrNotFound)

		currentTimestamp := newState.GetTimestamp()
		require.Equal(newValidatorStartTime.Unix(), currentTimestamp.Unix())
	}
}

func TestRejectedStateRegressionInvalidValidatorReward(t *testing.T) {
	require := require.New(t)

	vm, baseDB, mutableSharedMemory := defaultVM(t, cortina)
	vm.ctx.Lock.Lock()
	defer vm.ctx.Lock.Unlock()

	vm.state.SetCurrentSupply(constants.PrimaryNetworkID, defaultRewardConfig.SupplyCap/2)

	newValidatorStartTime0 := vm.clock.Time().Add(executor.SyncBound).Add(1 * time.Second)
	newValidatorEndTime0 := newValidatorStartTime0.Add(defaultMaxStakingDuration)

	nodeID0 := ids.GenerateTestNodeID()

	// Create the tx to add the first new validator
	addValidatorTx0, err := vm.txBuilder.NewAddValidatorTx(
		vm.MaxValidatorStake,
		uint64(newValidatorStartTime0.Unix()),
		uint64(newValidatorEndTime0.Unix()),
		nodeID0,
		ids.GenerateTestShortID(),
		reward.PercentDenominator,
		[]*secp256k1.PrivateKey{keys[0]},
		ids.ShortEmpty,
		nil,
	)
	require.NoError(err)

	// Create the standard block to add the first new validator
	preferredID := vm.manager.Preferred()
	preferred, err := vm.manager.GetBlock(preferredID)
	require.NoError(err)
	preferredChainTime := preferred.Timestamp()
	preferredHeight := preferred.Height()

	statelessAddValidatorStandardBlk0, err := block.NewBanffStandardBlock(
		preferredChainTime,
		preferredID,
		preferredHeight+1,
		[]*txs.Tx{addValidatorTx0},
	)
	require.NoError(err)

	addValidatorStandardBlk0 := vm.manager.NewBlock(statelessAddValidatorStandardBlk0)
	require.NoError(addValidatorStandardBlk0.Verify(context.Background()))

	// Verify that first new validator now in pending validator set
	{
		onAccept, ok := vm.manager.GetState(addValidatorStandardBlk0.ID())
		require.True(ok)

		_, err := onAccept.GetPendingValidator(constants.PrimaryNetworkID, nodeID0)
		require.NoError(err)
	}

	// Move chain time to bring the first new validator from the pending
	// validator set into the current validator set.
	vm.clock.Set(newValidatorStartTime0)

	// Create the proposal block that moves the first new validator from the
	// pending validator set into the current validator set.
	preferredID = addValidatorStandardBlk0.ID()
	preferredHeight = addValidatorStandardBlk0.Height()

	statelessAdvanceTimeStandardBlk0, err := block.NewBanffStandardBlock(
		newValidatorStartTime0,
		preferredID,
		preferredHeight+1,
		nil,
	)
	require.NoError(err)

	advanceTimeStandardBlk0 := vm.manager.NewBlock(statelessAdvanceTimeStandardBlk0)
	require.NoError(advanceTimeStandardBlk0.Verify(context.Background()))

	// Verify that the first new validator is now in the current validator set.
	{
		onAccept, ok := vm.manager.GetState(advanceTimeStandardBlk0.ID())
		require.True(ok)

		_, err := onAccept.GetCurrentValidator(constants.PrimaryNetworkID, nodeID0)
		require.NoError(err)

		_, err = onAccept.GetPendingValidator(constants.PrimaryNetworkID, nodeID0)
		require.ErrorIs(err, database.ErrNotFound)

		currentTimestamp := onAccept.GetTimestamp()
		require.Equal(newValidatorStartTime0.Unix(), currentTimestamp.Unix())
	}

	// Create the UTXO that will be added to shared memory
	utxo := &avax.UTXO{
		UTXOID: avax.UTXOID{
			TxID: ids.GenerateTestID(),
		},
		Asset: avax.Asset{
			ID: vm.ctx.AVAXAssetID,
		},
		Out: &secp256k1fx.TransferOutput{
			Amt:          vm.TxFee,
			OutputOwners: secp256k1fx.OutputOwners{},
		},
	}

	// Create the import tx that will fail verification
	unsignedImportTx := &txs.ImportTx{
		BaseTx: txs.BaseTx{BaseTx: avax.BaseTx{
			NetworkID:    vm.ctx.NetworkID,
			BlockchainID: vm.ctx.ChainID,
		}},
		SourceChain: vm.ctx.XChainID,
		ImportedInputs: []*avax.TransferableInput{
			{
				UTXOID: utxo.UTXOID,
				Asset:  utxo.Asset,
				In: &secp256k1fx.TransferInput{
					Amt: vm.TxFee,
				},
			},
		},
	}
	signedImportTx := &txs.Tx{Unsigned: unsignedImportTx}
	require.NoError(signedImportTx.Sign(txs.Codec, [][]*secp256k1.PrivateKey{
		{}, // There is one input, with no required signers
	}))

	// Create the standard block that will fail verification, and then be
	// re-verified.
	preferredChainTime = advanceTimeStandardBlk0.Timestamp()
	preferredID = advanceTimeStandardBlk0.ID()
	preferredHeight = advanceTimeStandardBlk0.Height()

	statelessImportBlk, err := block.NewBanffStandardBlock(
		preferredChainTime,
		preferredID,
		preferredHeight+1,
		[]*txs.Tx{signedImportTx},
	)
	require.NoError(err)

	importBlk := vm.manager.NewBlock(statelessImportBlk)
	// Because the shared memory UTXO hasn't been populated, this block is
	// currently invalid.
	err = importBlk.Verify(context.Background())
	require.ErrorIs(err, database.ErrNotFound)

	// Because we no longer ever reject a block in verification, the status
	// should remain as processing.
	importBlkStatus := importBlk.Status()
	require.Equal(choices.Processing, importBlkStatus)

	// Populate the shared memory UTXO.
	m := atomic.NewMemory(prefixdb.New([]byte{5}, baseDB))

	mutableSharedMemory.SharedMemory = m.NewSharedMemory(vm.ctx.ChainID)
	peerSharedMemory := m.NewSharedMemory(vm.ctx.XChainID)

	utxoBytes, err := txs.Codec.Marshal(txs.CodecVersion, utxo)
	require.NoError(err)

	inputID := utxo.InputID()
	require.NoError(peerSharedMemory.Apply(
		map[ids.ID]*atomic.Requests{
			vm.ctx.ChainID: {
				PutRequests: []*atomic.Element{
					{
						Key:   inputID[:],
						Value: utxoBytes,
					},
				},
			},
		},
	))

	// Because the shared memory UTXO has now been populated, the block should
	// pass verification.
	require.NoError(importBlk.Verify(context.Background()))

	// The status shouldn't have been changed during a successful verification.
	importBlkStatus = importBlk.Status()
	require.Equal(choices.Processing, importBlkStatus)

	newValidatorStartTime1 := newValidatorStartTime0.Add(executor.SyncBound).Add(1 * time.Second)
	newValidatorEndTime1 := newValidatorStartTime1.Add(defaultMaxStakingDuration)

	nodeID1 := ids.GenerateTestNodeID()

	// Create the tx to add the second new validator
	addValidatorTx1, err := vm.txBuilder.NewAddValidatorTx(
		vm.MaxValidatorStake,
		uint64(newValidatorStartTime1.Unix()),
		uint64(newValidatorEndTime1.Unix()),
		nodeID1,
		ids.GenerateTestShortID(),
		reward.PercentDenominator,
		[]*secp256k1.PrivateKey{keys[1]},
		ids.ShortEmpty,
		nil,
	)
	require.NoError(err)

	// Create the standard block to add the second new validator
	preferredChainTime = importBlk.Timestamp()
	preferredID = importBlk.ID()
	preferredHeight = importBlk.Height()

	statelessAddValidatorStandardBlk1, err := block.NewBanffStandardBlock(
		preferredChainTime,
		preferredID,
		preferredHeight+1,
		[]*txs.Tx{addValidatorTx1},
	)
	require.NoError(err)

	addValidatorStandardBlk1 := vm.manager.NewBlock(statelessAddValidatorStandardBlk1)

	require.NoError(addValidatorStandardBlk1.Verify(context.Background()))

	// Verify that the second new validator now in pending validator set
	{
		onAccept, ok := vm.manager.GetState(addValidatorStandardBlk1.ID())
		require.True(ok)

		_, err := onAccept.GetPendingValidator(constants.PrimaryNetworkID, nodeID1)
		require.NoError(err)
	}

	// Move chain time to bring the second new validator from the pending
	// validator set into the current validator set.
	vm.clock.Set(newValidatorStartTime1)

	// Create the proposal block that moves the second new validator from the
	// pending validator set into the current validator set.
	preferredID = addValidatorStandardBlk1.ID()
	preferredHeight = addValidatorStandardBlk1.Height()

	statelessAdvanceTimeStandardBlk1, err := block.NewBanffStandardBlock(
		newValidatorStartTime1,
		preferredID,
		preferredHeight+1,
		nil,
	)
	require.NoError(err)

	advanceTimeStandardBlk1 := vm.manager.NewBlock(statelessAdvanceTimeStandardBlk1)
	require.NoError(advanceTimeStandardBlk1.Verify(context.Background()))

	// Verify that the second new validator is now in the current validator set.
	{
		onAccept, ok := vm.manager.GetState(advanceTimeStandardBlk1.ID())
		require.True(ok)

		_, err := onAccept.GetCurrentValidator(constants.PrimaryNetworkID, nodeID1)
		require.NoError(err)

		_, err = onAccept.GetPendingValidator(constants.PrimaryNetworkID, nodeID1)
		require.ErrorIs(err, database.ErrNotFound)

		currentTimestamp := onAccept.GetTimestamp()
		require.Equal(newValidatorStartTime1.Unix(), currentTimestamp.Unix())
	}

	// Accept all the blocks
	allBlocks := []snowman.Block{
		addValidatorStandardBlk0,
		advanceTimeStandardBlk0,
		importBlk,
		addValidatorStandardBlk1,
		advanceTimeStandardBlk1,
	}
	for _, blk := range allBlocks {
		require.NoError(blk.Accept(context.Background()))

		status := blk.Status()
		require.Equal(choices.Accepted, status)
	}

	// Force a reload of the state from the database.
	vm.Config.Validators = validators.NewManager()
	execCfg, _ := config.GetExecutionConfig(nil)
	newState, err := state.New(
		vm.db,
		nil,
		prometheus.NewRegistry(),
		&vm.Config,
		execCfg,
		vm.ctx,
		metrics.Noop,
		reward.NewCalculator(vm.Config.RewardConfig),
	)
	require.NoError(err)

	// Verify that validators are in the current validator set with the correct
	// reward calculated.
	{
		staker0, err := newState.GetCurrentValidator(constants.PrimaryNetworkID, nodeID0)
		require.NoError(err)
		require.EqualValues(0, staker0.PotentialReward)

		staker1, err := newState.GetCurrentValidator(constants.PrimaryNetworkID, nodeID1)
		require.NoError(err)
		require.EqualValues(0, staker1.PotentialReward)

		_, err = newState.GetPendingValidator(constants.PrimaryNetworkID, nodeID0)
		require.ErrorIs(err, database.ErrNotFound)

		_, err = newState.GetPendingValidator(constants.PrimaryNetworkID, nodeID1)
		require.ErrorIs(err, database.ErrNotFound)

		currentTimestamp := newState.GetTimestamp()
		require.Equal(newValidatorStartTime1.Unix(), currentTimestamp.Unix())
	}
}

func TestValidatorSetAtCacheOverwriteRegression(t *testing.T) {
	require := require.New(t)

	vm, _, _ := defaultVM(t, cortina)
	vm.ctx.Lock.Lock()
	defer vm.ctx.Lock.Unlock()

	currentHeight, err := vm.GetCurrentHeight(context.Background())
	require.NoError(err)
	require.Equal(uint64(1), currentHeight)

	expectedValidators1 := map[ids.NodeID]uint64{
		genesisNodeIDs[0]: defaultWeight,
		genesisNodeIDs[1]: defaultWeight,
		genesisNodeIDs[2]: defaultWeight,
		genesisNodeIDs[3]: defaultWeight,
		genesisNodeIDs[4]: defaultWeight,
	}
	validators, err := vm.GetValidatorSet(context.Background(), 1, constants.PrimaryNetworkID)
	require.NoError(err)
	for nodeID, weight := range expectedValidators1 {
		require.Equal(weight, validators[nodeID].Weight)
	}

	newValidatorStartTime0 := vm.clock.Time().Add(executor.SyncBound).Add(1 * time.Second)
	newValidatorEndTime0 := newValidatorStartTime0.Add(defaultMaxStakingDuration)

	extraNodeID := ids.GenerateTestNodeID()

	// Create the tx to add the first new validator
	addValidatorTx0, err := vm.txBuilder.NewAddValidatorTx(
		vm.MaxValidatorStake,
		uint64(newValidatorStartTime0.Unix()),
		uint64(newValidatorEndTime0.Unix()),
		extraNodeID,
		ids.GenerateTestShortID(),
		reward.PercentDenominator,
		[]*secp256k1.PrivateKey{keys[0]},
		ids.GenerateTestShortID(),
		nil,
	)
	require.NoError(err)

	// Create the standard block to add the first new validator
	preferredID := vm.manager.Preferred()
	preferred, err := vm.manager.GetBlock(preferredID)
	require.NoError(err)
	preferredChainTime := preferred.Timestamp()
	preferredHeight := preferred.Height()

	statelessStandardBlk, err := block.NewBanffStandardBlock(
		preferredChainTime,
		preferredID,
		preferredHeight+1,
		[]*txs.Tx{addValidatorTx0},
	)
	require.NoError(err)
	addValidatorProposalBlk0 := vm.manager.NewBlock(statelessStandardBlk)
	require.NoError(addValidatorProposalBlk0.Verify(context.Background()))
	require.NoError(addValidatorProposalBlk0.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	currentHeight, err = vm.GetCurrentHeight(context.Background())
	require.NoError(err)
	require.Equal(uint64(2), currentHeight)

	for i := uint64(1); i <= 2; i++ {
		validators, err = vm.GetValidatorSet(context.Background(), i, constants.PrimaryNetworkID)
		require.NoError(err)
		for nodeID, weight := range expectedValidators1 {
			require.Equal(weight, validators[nodeID].Weight)
		}
	}

	// Advance chain time to move the first new validator from the pending
	// validator set into the current validator set.
	vm.clock.Set(newValidatorStartTime0)

	// Create the standard block that moves the first new validator from the
	// pending validator set into the current validator set.
	preferredID = vm.manager.Preferred()
	preferred, err = vm.manager.GetBlock(preferredID)
	require.NoError(err)
	preferredID = preferred.ID()
	preferredHeight = preferred.Height()

	statelessStandardBlk, err = block.NewBanffStandardBlock(
		newValidatorStartTime0,
		preferredID,
		preferredHeight+1,
		nil,
	)
	require.NoError(err)
	advanceTimeProposalBlk0 := vm.manager.NewBlock(statelessStandardBlk)
	require.NoError(advanceTimeProposalBlk0.Verify(context.Background()))
	require.NoError(advanceTimeProposalBlk0.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	currentHeight, err = vm.GetCurrentHeight(context.Background())
	require.NoError(err)
	require.Equal(uint64(3), currentHeight)

	for i := uint64(1); i <= 2; i++ {
		validators, err = vm.GetValidatorSet(context.Background(), i, constants.PrimaryNetworkID)
		require.NoError(err)
		for nodeID, weight := range expectedValidators1 {
			require.Equal(weight, validators[nodeID].Weight)
		}
	}

	expectedValidators2 := map[ids.NodeID]uint64{
		genesisNodeIDs[0]: defaultWeight,
		genesisNodeIDs[1]: defaultWeight,
		genesisNodeIDs[2]: defaultWeight,
		genesisNodeIDs[3]: defaultWeight,
		genesisNodeIDs[4]: defaultWeight,
		extraNodeID:       vm.MaxValidatorStake,
	}
	validators, err = vm.GetValidatorSet(context.Background(), 3, constants.PrimaryNetworkID)
	require.NoError(err)
	for nodeID, weight := range expectedValidators2 {
		require.Equal(weight, validators[nodeID].Weight)
	}
}

func TestAddDelegatorTxAddBeforeRemove(t *testing.T) {
	require := require.New(t)

	validatorStartTime := latestForkTime.Add(executor.SyncBound).Add(1 * time.Second)
	validatorEndTime := validatorStartTime.Add(360 * 24 * time.Hour)
	validatorStake := defaultMaxValidatorStake / 5

	delegator1StartTime := validatorStartTime
	delegator1EndTime := delegator1StartTime.Add(3 * defaultMinStakingDuration)
	delegator1Stake := defaultMaxValidatorStake - validatorStake

	delegator2StartTime := delegator1EndTime
	delegator2EndTime := delegator2StartTime.Add(3 * defaultMinStakingDuration)
	delegator2Stake := defaultMaxValidatorStake - validatorStake

	vm, _, _ := defaultVM(t, cortina)
	vm.ctx.Lock.Lock()
	defer vm.ctx.Lock.Unlock()

	key, err := secp256k1.NewPrivateKey()
	require.NoError(err)

	id := key.Address()
	nodeID := ids.GenerateTestNodeID()
	changeAddr := keys[0].PublicKey().Address()

	// create valid tx
	addValidatorTx, err := vm.txBuilder.NewAddValidatorTx(
		validatorStake,
		uint64(validatorStartTime.Unix()),
		uint64(validatorEndTime.Unix()),
		nodeID,
		id,
		reward.PercentDenominator,
		[]*secp256k1.PrivateKey{keys[0], keys[1]},
		changeAddr,
		nil,
	)
	require.NoError(err)

	// issue the add validator tx
	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), addValidatorTx))
	vm.ctx.Lock.Lock()

	// trigger block creation for the validator tx
	addValidatorBlock, err := vm.Builder.BuildBlock(context.Background())
	require.NoError(err)
	require.NoError(addValidatorBlock.Verify(context.Background()))
	require.NoError(addValidatorBlock.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	// create valid tx
	addFirstDelegatorTx, err := vm.txBuilder.NewAddDelegatorTx(
		delegator1Stake,
		uint64(delegator1StartTime.Unix()),
		uint64(delegator1EndTime.Unix()),
		nodeID,
		keys[0].PublicKey().Address(),
		[]*secp256k1.PrivateKey{keys[0], keys[1]},
		changeAddr,
		nil,
	)
	require.NoError(err)

	// issue the first add delegator tx
	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), addFirstDelegatorTx))
	vm.ctx.Lock.Lock()

	// trigger block creation for the first add delegator tx
	addFirstDelegatorBlock, err := vm.Builder.BuildBlock(context.Background())
	require.NoError(err)
	require.NoError(addFirstDelegatorBlock.Verify(context.Background()))
	require.NoError(addFirstDelegatorBlock.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	// create valid tx
	addSecondDelegatorTx, err := vm.txBuilder.NewAddDelegatorTx(
		delegator2Stake,
		uint64(delegator2StartTime.Unix()),
		uint64(delegator2EndTime.Unix()),
		nodeID,
		keys[0].PublicKey().Address(),
		[]*secp256k1.PrivateKey{keys[0], keys[1]},
		changeAddr,
		nil,
	)
	require.NoError(err)

	// attempting to issue the second add delegator tx should fail because the
	// total stake weight would go over the limit.
	vm.ctx.Lock.Unlock()
	err = vm.issueTx(context.Background(), addSecondDelegatorTx)
	require.ErrorIs(err, executor.ErrOverDelegated)
	vm.ctx.Lock.Lock()
}

func TestRemovePermissionedValidatorDuringPendingToCurrentTransitionNotTracked(t *testing.T) {
	require := require.New(t)

	validatorStartTime := latestForkTime.Add(executor.SyncBound).Add(1 * time.Second)
	validatorEndTime := validatorStartTime.Add(360 * 24 * time.Hour)

	vm, _, _ := defaultVM(t, cortina)
	vm.ctx.Lock.Lock()
	defer vm.ctx.Lock.Unlock()

	key, err := secp256k1.NewPrivateKey()
	require.NoError(err)

	id := key.Address()
	nodeID := ids.GenerateTestNodeID()
	changeAddr := keys[0].PublicKey().Address()

	addValidatorTx, err := vm.txBuilder.NewAddValidatorTx(
		defaultMaxValidatorStake,
		uint64(validatorStartTime.Unix()),
		uint64(validatorEndTime.Unix()),
		nodeID,
		id,
		reward.PercentDenominator,
		[]*secp256k1.PrivateKey{keys[0], keys[1]},
		changeAddr,
		nil,
	)
	require.NoError(err)

	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), addValidatorTx))
	vm.ctx.Lock.Lock()

	// trigger block creation for the validator tx
	addValidatorBlock, err := vm.Builder.BuildBlock(context.Background())
	require.NoError(err)
	require.NoError(addValidatorBlock.Verify(context.Background()))
	require.NoError(addValidatorBlock.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	createSubnetTx, err := vm.txBuilder.NewCreateSubnetTx(
		1,
		[]ids.ShortID{changeAddr},
		[]*secp256k1.PrivateKey{keys[0], keys[1]},
		changeAddr,
		nil,
	)
	require.NoError(err)

	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), createSubnetTx))
	vm.ctx.Lock.Lock()

	// trigger block creation for the subnet tx
	createSubnetBlock, err := vm.Builder.BuildBlock(context.Background())
	require.NoError(err)
	require.NoError(createSubnetBlock.Verify(context.Background()))
	require.NoError(createSubnetBlock.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	addSubnetValidatorTx, err := vm.txBuilder.NewAddSubnetValidatorTx(
		defaultMaxValidatorStake,
		uint64(validatorStartTime.Unix()),
		uint64(validatorEndTime.Unix()),
		nodeID,
		createSubnetTx.ID(),
		[]*secp256k1.PrivateKey{keys[0], keys[1]},
		changeAddr,
		nil,
	)
	require.NoError(err)

	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), addSubnetValidatorTx))
	vm.ctx.Lock.Lock()

	// trigger block creation for the validator tx
	addSubnetValidatorBlock, err := vm.Builder.BuildBlock(context.Background())
	require.NoError(err)
	require.NoError(addSubnetValidatorBlock.Verify(context.Background()))
	require.NoError(addSubnetValidatorBlock.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	emptyValidatorSet, err := vm.GetValidatorSet(
		context.Background(),
		addSubnetValidatorBlock.Height(),
		createSubnetTx.ID(),
	)
	require.NoError(err)
	require.Empty(emptyValidatorSet)

	removeSubnetValidatorTx, err := vm.txBuilder.NewRemoveSubnetValidatorTx(
		nodeID,
		createSubnetTx.ID(),
		[]*secp256k1.PrivateKey{keys[0], keys[1]},
		changeAddr,
		nil,
	)
	require.NoError(err)

	// Set the clock so that the validator will be moved from the pending
	// validator set into the current validator set.
	vm.clock.Set(validatorStartTime)

	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), removeSubnetValidatorTx))
	vm.ctx.Lock.Lock()

	// trigger block creation for the validator tx
	removeSubnetValidatorBlock, err := vm.Builder.BuildBlock(context.Background())
	require.NoError(err)
	require.NoError(removeSubnetValidatorBlock.Verify(context.Background()))
	require.NoError(removeSubnetValidatorBlock.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	emptyValidatorSet, err = vm.GetValidatorSet(
		context.Background(),
		addSubnetValidatorBlock.Height(),
		createSubnetTx.ID(),
	)
	require.NoError(err)
	require.Empty(emptyValidatorSet)
}

func TestRemovePermissionedValidatorDuringPendingToCurrentTransitionTracked(t *testing.T) {
	require := require.New(t)

	validatorStartTime := latestForkTime.Add(executor.SyncBound).Add(1 * time.Second)
	validatorEndTime := validatorStartTime.Add(360 * 24 * time.Hour)

	vm, _, _ := defaultVM(t, cortina)
	vm.ctx.Lock.Lock()
	defer vm.ctx.Lock.Unlock()

	key, err := secp256k1.NewPrivateKey()
	require.NoError(err)

	id := key.PublicKey().Address()
	nodeID := ids.GenerateTestNodeID()
	changeAddr := keys[0].PublicKey().Address()

	addValidatorTx, err := vm.txBuilder.NewAddValidatorTx(
		defaultMaxValidatorStake,
		uint64(validatorStartTime.Unix()),
		uint64(validatorEndTime.Unix()),
		nodeID,
		id,
		reward.PercentDenominator,
		[]*secp256k1.PrivateKey{keys[0], keys[1]},
		changeAddr,
		nil,
	)
	require.NoError(err)

	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), addValidatorTx))
	vm.ctx.Lock.Lock()

	// trigger block creation for the validator tx
	addValidatorBlock, err := vm.Builder.BuildBlock(context.Background())
	require.NoError(err)
	require.NoError(addValidatorBlock.Verify(context.Background()))
	require.NoError(addValidatorBlock.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	createSubnetTx, err := vm.txBuilder.NewCreateSubnetTx(
		1,
		[]ids.ShortID{changeAddr},
		[]*secp256k1.PrivateKey{keys[0], keys[1]},
		changeAddr,
		nil,
	)
	require.NoError(err)

	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), createSubnetTx))
	vm.ctx.Lock.Lock()

	// trigger block creation for the subnet tx
	createSubnetBlock, err := vm.Builder.BuildBlock(context.Background())
	require.NoError(err)
	require.NoError(createSubnetBlock.Verify(context.Background()))
	require.NoError(createSubnetBlock.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	addSubnetValidatorTx, err := vm.txBuilder.NewAddSubnetValidatorTx(
		defaultMaxValidatorStake,
		uint64(validatorStartTime.Unix()),
		uint64(validatorEndTime.Unix()),
		nodeID,
		createSubnetTx.ID(),
		[]*secp256k1.PrivateKey{keys[0], keys[1]},
		changeAddr,
		nil,
	)
	require.NoError(err)

	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), addSubnetValidatorTx))
	vm.ctx.Lock.Lock()

	// trigger block creation for the validator tx
	addSubnetValidatorBlock, err := vm.Builder.BuildBlock(context.Background())
	require.NoError(err)
	require.NoError(addSubnetValidatorBlock.Verify(context.Background()))
	require.NoError(addSubnetValidatorBlock.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	removeSubnetValidatorTx, err := vm.txBuilder.NewRemoveSubnetValidatorTx(
		nodeID,
		createSubnetTx.ID(),
		[]*secp256k1.PrivateKey{keys[0], keys[1]},
		changeAddr,
		nil,
	)
	require.NoError(err)

	// Set the clock so that the validator will be moved from the pending
	// validator set into the current validator set.
	vm.clock.Set(validatorStartTime)

	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), removeSubnetValidatorTx))
	vm.ctx.Lock.Lock()

	// trigger block creation for the validator tx
	removeSubnetValidatorBlock, err := vm.Builder.BuildBlock(context.Background())
	require.NoError(err)
	require.NoError(removeSubnetValidatorBlock.Verify(context.Background()))
	require.NoError(removeSubnetValidatorBlock.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))
}

// GetValidatorSet must return the BLS keys for a given validator correctly when
// queried at a previous height, even in case it has currently expired
func TestSubnetValidatorBLSKeyDiffAfterExpiry(t *testing.T) {
	// setup
	require := require.New(t)
	vm, _, _ := defaultVM(t, cortina)
	vm.ctx.Lock.Lock()
	defer vm.ctx.Lock.Unlock()

	subnetID := testSubnet1.TxID

	// setup time
	currentTime := defaultGenesisTime
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)

	// A subnet validator stakes and then stops; also its primary network counterpart stops staking
	var (
		primaryStartTime   = currentTime.Add(executor.SyncBound)
		subnetStartTime    = primaryStartTime.Add(executor.SyncBound)
		subnetEndTime      = subnetStartTime.Add(defaultMinStakingDuration)
		primaryEndTime     = subnetEndTime.Add(time.Second)
		primaryReStartTime = primaryEndTime.Add(executor.SyncBound)
		primaryReEndTime   = primaryReStartTime.Add(defaultMinStakingDuration)
	)

	// insert primary network validator
	var (
		nodeID = ids.GenerateTestNodeID()
		addr   = keys[0].PublicKey().Address()
	)
	sk1, err := bls.NewSecretKey()
	require.NoError(err)

	// build primary network validator with BLS key
	primaryTx, err := vm.txBuilder.NewAddPermissionlessValidatorTx(
		vm.MinValidatorStake,
		uint64(primaryStartTime.Unix()),
		uint64(primaryEndTime.Unix()),
		nodeID,
		signer.NewProofOfPossession(sk1),
		addr, // reward address
		reward.PercentDenominator,
		keys,
		addr, // change address
		nil,
	)
	require.NoError(err)
	uPrimaryTx := primaryTx.Unsigned.(*txs.AddPermissionlessValidatorTx)

	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), primaryTx))
	vm.ctx.Lock.Lock()
	require.NoError(buildAndAcceptStandardBlock(vm))

	// move time ahead, promoting primary validator to current
	currentTime = primaryStartTime
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)
	require.NoError(buildAndAcceptStandardBlock(vm))

	_, err = vm.state.GetCurrentValidator(constants.PrimaryNetworkID, nodeID)
	require.NoError(err)

	primaryStartHeight, err := vm.GetCurrentHeight(context.Background())
	require.NoError(err)

	// insert the subnet validator
	subnetTx, err := vm.txBuilder.NewAddSubnetValidatorTx(
		1,                              // Weight
		uint64(subnetStartTime.Unix()), // Start time
		uint64(subnetEndTime.Unix()),   // end time
		nodeID,                         // Node ID
		subnetID,
		[]*secp256k1.PrivateKey{keys[0], keys[1]},
		addr,
		nil,
	)
	require.NoError(err)

	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), subnetTx))
	vm.ctx.Lock.Lock()
	require.NoError(buildAndAcceptStandardBlock(vm))

	// move time ahead, promoting the subnet validator to current
	currentTime = subnetStartTime
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)
	require.NoError(buildAndAcceptStandardBlock(vm))

	_, err = vm.state.GetCurrentValidator(subnetID, nodeID)
	require.NoError(err)

	subnetStartHeight, err := vm.GetCurrentHeight(context.Background())
	require.NoError(err)

	// move time ahead, terminating the subnet validator
	currentTime = subnetEndTime
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)
	require.NoError(buildAndAcceptStandardBlock(vm))

	_, err = vm.state.GetCurrentValidator(subnetID, nodeID)
	require.ErrorIs(err, database.ErrNotFound)

	subnetEndHeight, err := vm.GetCurrentHeight(context.Background())
	require.NoError(err)

	// move time ahead, terminating primary network validator
	currentTime = primaryEndTime
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)

	blk, err := vm.Builder.BuildBlock(context.Background()) // must be a proposal block rewarding the primary validator
	require.NoError(err)
	require.NoError(blk.Verify(context.Background()))

	proposalBlk := blk.(snowman.OracleBlock)
	options, err := proposalBlk.Options(context.Background())
	require.NoError(err)

	commit := options[0].(*blockexecutor.Block)
	require.IsType(&block.BanffCommitBlock{}, commit.Block)

	require.NoError(blk.Accept(context.Background()))
	require.NoError(commit.Verify(context.Background()))
	require.NoError(commit.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	_, err = vm.state.GetCurrentValidator(constants.PrimaryNetworkID, nodeID)
	require.ErrorIs(err, database.ErrNotFound)

	primaryEndHeight, err := vm.GetCurrentHeight(context.Background())
	require.NoError(err)

	// reinsert primary validator with a different BLS key
	sk2, err := bls.NewSecretKey()
	require.NoError(err)
	require.NotEqual(sk1, sk2)

	primaryRestartTx, err := vm.txBuilder.NewAddPermissionlessValidatorTx(
		vm.MinValidatorStake,
		uint64(primaryReStartTime.Unix()),
		uint64(primaryReEndTime.Unix()),
		nodeID,
		signer.NewProofOfPossession(sk2),
		addr, // reward address
		reward.PercentDenominator,
		keys,
		addr, // change address
		nil,
	)
	require.NoError(err)
	uPrimaryRestartTx := primaryRestartTx.Unsigned.(*txs.AddPermissionlessValidatorTx)

	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), primaryRestartTx))
	vm.ctx.Lock.Lock()
	require.NoError(buildAndAcceptStandardBlock(vm))

	// move time ahead, promoting restarted primary validator to current
	currentTime = primaryReStartTime
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)
	require.NoError(buildAndAcceptStandardBlock(vm))

	_, err = vm.state.GetCurrentValidator(constants.PrimaryNetworkID, nodeID)
	require.NoError(err)

	primaryRestartHeight, err := vm.GetCurrentHeight(context.Background())
	require.NoError(err)

	// Show that validators are rebuilt with the right BLS key
	for height := primaryStartHeight; height < primaryEndHeight; height++ {
		require.NoError(checkValidatorBlsKeyIsSet(
			vm.State,
			nodeID,
			constants.PrimaryNetworkID,
			height,
			uPrimaryTx.Signer.Key(),
		))
	}
	for height := primaryEndHeight; height < primaryRestartHeight; height++ {
		err := checkValidatorBlsKeyIsSet(
			vm.State,
			nodeID,
			constants.PrimaryNetworkID,
			primaryEndHeight,
			uPrimaryTx.Signer.Key(),
		)
		require.ErrorIs(err, database.ErrNotFound)
	}
	require.NoError(checkValidatorBlsKeyIsSet(
		vm.State,
		nodeID,
		constants.PrimaryNetworkID,
		primaryRestartHeight,
		uPrimaryRestartTx.Signer.Key(),
	))

	for height := subnetStartHeight; height < subnetEndHeight; height++ {
		require.NoError(checkValidatorBlsKeyIsSet(
			vm.State,
			nodeID,
			subnetID,
			height,
			uPrimaryTx.Signer.Key(),
		))
	}

	for height := subnetEndHeight; height <= primaryRestartHeight; height++ {
		err := checkValidatorBlsKeyIsSet(
			vm.State,
			nodeID,
			subnetID,
			primaryEndHeight,
			uPrimaryTx.Signer.Key(),
		)
		require.ErrorIs(err, database.ErrNotFound)
	}
}

func TestPrimaryNetworkValidatorPopulatedToEmptyBLSKeyDiff(t *testing.T) {
	// A primary network validator has an empty BLS key. Then it restakes adding
	// the BLS key. Querying the validator set back when BLS key was empty must
	// return an empty BLS key.

	// setup
	require := require.New(t)
	vm, _, _ := defaultVM(t, cortina)
	vm.ctx.Lock.Lock()
	defer vm.ctx.Lock.Unlock()

	// setup time
	currentTime := defaultGenesisTime
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)

	// A primary network validator stake twice
	var (
		primaryStartTime1 = currentTime.Add(executor.SyncBound)
		primaryEndTime1   = primaryStartTime1.Add(defaultMinStakingDuration)
		primaryStartTime2 = primaryEndTime1.Add(executor.SyncBound)
		primaryEndTime2   = primaryStartTime2.Add(defaultMinStakingDuration)
	)

	// Add a primary network validator with no BLS key
	nodeID := ids.GenerateTestNodeID()
	addr := keys[0].PublicKey().Address()
	primaryTx1, err := vm.txBuilder.NewAddValidatorTx(
		vm.MinValidatorStake,
		uint64(primaryStartTime1.Unix()),
		uint64(primaryEndTime1.Unix()),
		nodeID,
		addr,
		reward.PercentDenominator,
		[]*secp256k1.PrivateKey{keys[0]},
		addr,
		nil,
	)
	require.NoError(err)

	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), primaryTx1))
	vm.ctx.Lock.Lock()
	require.NoError(buildAndAcceptStandardBlock(vm))

	// move time ahead, promoting primary validator to current
	currentTime = primaryStartTime1
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)
	require.NoError(buildAndAcceptStandardBlock(vm))

	_, err = vm.state.GetCurrentValidator(constants.PrimaryNetworkID, nodeID)
	require.NoError(err)

	primaryStartHeight, err := vm.GetCurrentHeight(context.Background())
	require.NoError(err)

	// move time ahead, terminating primary network validator
	currentTime = primaryEndTime1
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)

	blk, err := vm.Builder.BuildBlock(context.Background()) // must be a proposal block rewarding the primary validator
	require.NoError(err)
	require.NoError(blk.Verify(context.Background()))

	proposalBlk := blk.(snowman.OracleBlock)
	options, err := proposalBlk.Options(context.Background())
	require.NoError(err)

	commit := options[0].(*blockexecutor.Block)
	require.IsType(&block.BanffCommitBlock{}, commit.Block)

	require.NoError(blk.Accept(context.Background()))
	require.NoError(commit.Verify(context.Background()))
	require.NoError(commit.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	_, err = vm.state.GetCurrentValidator(constants.PrimaryNetworkID, nodeID)
	require.ErrorIs(err, database.ErrNotFound)

	primaryEndHeight, err := vm.GetCurrentHeight(context.Background())
	require.NoError(err)

	// reinsert primary validator with a different BLS key
	sk2, err := bls.NewSecretKey()
	require.NoError(err)

	primaryRestartTx, err := vm.txBuilder.NewAddPermissionlessValidatorTx(
		vm.MinValidatorStake,
		uint64(primaryStartTime2.Unix()),
		uint64(primaryEndTime2.Unix()),
		nodeID,
		signer.NewProofOfPossession(sk2),
		addr, // reward address
		reward.PercentDenominator,
		keys,
		addr, // change address
		nil,
	)
	require.NoError(err)

	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), primaryRestartTx))
	vm.ctx.Lock.Lock()
	require.NoError(buildAndAcceptStandardBlock(vm))

	// move time ahead, promoting restarted primary validator to current
	currentTime = primaryStartTime2
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)
	require.NoError(buildAndAcceptStandardBlock(vm))

	_, err = vm.state.GetCurrentValidator(constants.PrimaryNetworkID, nodeID)
	require.NoError(err)

	emptySigner := &signer.Empty{}
	for height := primaryStartHeight; height < primaryEndHeight; height++ {
		require.NoError(checkValidatorBlsKeyIsSet(
			vm.State,
			nodeID,
			constants.PrimaryNetworkID,
			height,
			emptySigner.Key(),
		))
	}
}

func TestSubnetValidatorPopulatedToEmptyBLSKeyDiff(t *testing.T) {
	// A primary network validator has an empty BLS key and a subnet validator.
	// Primary network validator terminates its first staking cycle and it
	// restakes adding the BLS key. Querying the validator set back when BLS key
	// was empty must return an empty BLS key for the subnet validator

	// setup
	require := require.New(t)
	vm, _, _ := defaultVM(t, cortina)
	vm.ctx.Lock.Lock()
	defer vm.ctx.Lock.Unlock()

	subnetID := testSubnet1.TxID

	// setup time
	currentTime := defaultGenesisTime
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)

	// A primary network validator stake twice
	var (
		primaryStartTime1 = currentTime.Add(executor.SyncBound)
		subnetStartTime   = primaryStartTime1.Add(executor.SyncBound)
		subnetEndTime     = subnetStartTime.Add(defaultMinStakingDuration)
		primaryEndTime1   = subnetEndTime.Add(time.Second)
		primaryStartTime2 = primaryEndTime1.Add(executor.SyncBound)
		primaryEndTime2   = primaryStartTime2.Add(defaultMinStakingDuration)
	)

	// Add a primary network validator with no BLS key
	nodeID := ids.GenerateTestNodeID()
	addr := keys[0].PublicKey().Address()
	primaryTx1, err := vm.txBuilder.NewAddValidatorTx(
		vm.MinValidatorStake,
		uint64(primaryStartTime1.Unix()),
		uint64(primaryEndTime1.Unix()),
		nodeID,
		addr,
		reward.PercentDenominator,
		[]*secp256k1.PrivateKey{keys[0]},
		addr,
		nil,
	)
	require.NoError(err)

	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), primaryTx1))
	vm.ctx.Lock.Lock()
	require.NoError(buildAndAcceptStandardBlock(vm))

	// move time ahead, promoting primary validator to current
	currentTime = primaryStartTime1
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)
	require.NoError(buildAndAcceptStandardBlock(vm))

	_, err = vm.state.GetCurrentValidator(constants.PrimaryNetworkID, nodeID)
	require.NoError(err)

	primaryStartHeight, err := vm.GetCurrentHeight(context.Background())
	require.NoError(err)

	// insert the subnet validator
	subnetTx, err := vm.txBuilder.NewAddSubnetValidatorTx(
		1,                              // Weight
		uint64(subnetStartTime.Unix()), // Start time
		uint64(subnetEndTime.Unix()),   // end time
		nodeID,                         // Node ID
		subnetID,
		[]*secp256k1.PrivateKey{keys[0], keys[1]},
		addr,
		nil,
	)
	require.NoError(err)

	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), subnetTx))
	vm.ctx.Lock.Lock()
	require.NoError(buildAndAcceptStandardBlock(vm))

	// move time ahead, promoting the subnet validator to current
	currentTime = subnetStartTime
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)
	require.NoError(buildAndAcceptStandardBlock(vm))

	_, err = vm.state.GetCurrentValidator(subnetID, nodeID)
	require.NoError(err)

	subnetStartHeight, err := vm.GetCurrentHeight(context.Background())
	require.NoError(err)

	// move time ahead, terminating the subnet validator
	currentTime = subnetEndTime
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)
	require.NoError(buildAndAcceptStandardBlock(vm))

	_, err = vm.state.GetCurrentValidator(subnetID, nodeID)
	require.ErrorIs(err, database.ErrNotFound)

	subnetEndHeight, err := vm.GetCurrentHeight(context.Background())
	require.NoError(err)

	// move time ahead, terminating primary network validator
	currentTime = primaryEndTime1
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)

	blk, err := vm.Builder.BuildBlock(context.Background()) // must be a proposal block rewarding the primary validator
	require.NoError(err)
	require.NoError(blk.Verify(context.Background()))

	proposalBlk := blk.(snowman.OracleBlock)
	options, err := proposalBlk.Options(context.Background())
	require.NoError(err)

	commit := options[0].(*blockexecutor.Block)
	require.IsType(&block.BanffCommitBlock{}, commit.Block)

	require.NoError(blk.Accept(context.Background()))
	require.NoError(commit.Verify(context.Background()))
	require.NoError(commit.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	_, err = vm.state.GetCurrentValidator(constants.PrimaryNetworkID, nodeID)
	require.ErrorIs(err, database.ErrNotFound)

	primaryEndHeight, err := vm.GetCurrentHeight(context.Background())
	require.NoError(err)

	// reinsert primary validator with a different BLS key
	sk2, err := bls.NewSecretKey()
	require.NoError(err)

	primaryRestartTx, err := vm.txBuilder.NewAddPermissionlessValidatorTx(
		vm.MinValidatorStake,
		uint64(primaryStartTime2.Unix()),
		uint64(primaryEndTime2.Unix()),
		nodeID,
		signer.NewProofOfPossession(sk2),
		addr, // reward address
		reward.PercentDenominator,
		keys,
		addr, // change address
		nil,
	)
	require.NoError(err)

	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), primaryRestartTx))
	vm.ctx.Lock.Lock()
	require.NoError(buildAndAcceptStandardBlock(vm))

	// move time ahead, promoting restarted primary validator to current
	currentTime = primaryStartTime2
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)

	require.NoError(buildAndAcceptStandardBlock(vm))
	_, err = vm.state.GetCurrentValidator(constants.PrimaryNetworkID, nodeID)
	require.NoError(err)

	emptySigner := &signer.Empty{}
	for height := primaryStartHeight; height < primaryEndHeight; height++ {
		require.NoError(checkValidatorBlsKeyIsSet(
			vm.State,
			nodeID,
			constants.PrimaryNetworkID,
			height,
			emptySigner.Key(),
		))
	}
	for height := subnetStartHeight; height < subnetEndHeight; height++ {
		require.NoError(checkValidatorBlsKeyIsSet(
			vm.State,
			nodeID,
			subnetID,
			height,
			emptySigner.Key(),
		))
	}
}

func TestSubnetValidatorSetAfterPrimaryNetworkValidatorRemoval(t *testing.T) {
	// A primary network validator and a subnet validator are running.
	// Primary network validator terminates its staking cycle.
	// Querying the validator set when the subnet validator existed should
	// succeed.

	// setup
	require := require.New(t)
	vm, _, _ := defaultVM(t, cortina)
	vm.ctx.Lock.Lock()
	defer vm.ctx.Lock.Unlock()

	subnetID := testSubnet1.TxID

	// setup time
	currentTime := defaultGenesisTime
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)

	// A primary network validator stake twice
	var (
		primaryStartTime1 = currentTime.Add(executor.SyncBound)
		subnetStartTime   = primaryStartTime1.Add(executor.SyncBound)
		subnetEndTime     = subnetStartTime.Add(defaultMinStakingDuration)
		primaryEndTime1   = subnetEndTime.Add(time.Second)
	)

	// Add a primary network validator with no BLS key
	nodeID := ids.GenerateTestNodeID()
	addr := keys[0].PublicKey().Address()
	primaryTx1, err := vm.txBuilder.NewAddValidatorTx(
		vm.MinValidatorStake,
		uint64(primaryStartTime1.Unix()),
		uint64(primaryEndTime1.Unix()),
		nodeID,
		addr,
		reward.PercentDenominator,
		[]*secp256k1.PrivateKey{keys[0]},
		addr,
		nil,
	)
	require.NoError(err)

	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), primaryTx1))
	vm.ctx.Lock.Lock()
	require.NoError(buildAndAcceptStandardBlock(vm))

	// move time ahead, promoting primary validator to current
	currentTime = primaryStartTime1
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)
	require.NoError(buildAndAcceptStandardBlock(vm))

	_, err = vm.state.GetCurrentValidator(constants.PrimaryNetworkID, nodeID)
	require.NoError(err)

	// insert the subnet validator
	subnetTx, err := vm.txBuilder.NewAddSubnetValidatorTx(
		1,                              // Weight
		uint64(subnetStartTime.Unix()), // Start time
		uint64(subnetEndTime.Unix()),   // end time
		nodeID,                         // Node ID
		subnetID,
		[]*secp256k1.PrivateKey{keys[0], keys[1]},
		addr,
		nil,
	)
	require.NoError(err)

	vm.ctx.Lock.Unlock()
	require.NoError(vm.issueTx(context.Background(), subnetTx))
	vm.ctx.Lock.Lock()
	require.NoError(buildAndAcceptStandardBlock(vm))

	// move time ahead, promoting the subnet validator to current
	currentTime = subnetStartTime
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)
	require.NoError(buildAndAcceptStandardBlock(vm))

	_, err = vm.state.GetCurrentValidator(subnetID, nodeID)
	require.NoError(err)

	subnetStartHeight, err := vm.GetCurrentHeight(context.Background())
	require.NoError(err)

	// move time ahead, terminating the subnet validator
	currentTime = subnetEndTime
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)
	require.NoError(buildAndAcceptStandardBlock(vm))

	_, err = vm.state.GetCurrentValidator(subnetID, nodeID)
	require.ErrorIs(err, database.ErrNotFound)

	// move time ahead, terminating primary network validator
	currentTime = primaryEndTime1
	vm.clock.Set(currentTime)
	vm.state.SetTimestamp(currentTime)

	blk, err := vm.Builder.BuildBlock(context.Background()) // must be a proposal block rewarding the primary validator
	require.NoError(err)
	require.NoError(blk.Verify(context.Background()))

	proposalBlk := blk.(snowman.OracleBlock)
	options, err := proposalBlk.Options(context.Background())
	require.NoError(err)

	commit := options[0].(*blockexecutor.Block)
	require.IsType(&block.BanffCommitBlock{}, commit.Block)

	require.NoError(blk.Accept(context.Background()))
	require.NoError(commit.Verify(context.Background()))
	require.NoError(commit.Accept(context.Background()))
	require.NoError(vm.SetPreference(context.Background(), vm.manager.LastAccepted()))

	_, err = vm.state.GetCurrentValidator(constants.PrimaryNetworkID, nodeID)
	require.ErrorIs(err, database.ErrNotFound)

	// Generating the validator set should not error when re-introducing a
	// subnet validator whose primary network validator was also removed.
	_, err = vm.State.GetValidatorSet(context.Background(), subnetStartHeight, subnetID)
	require.NoError(err)
}

func TestValidatorSetRaceCondition(t *testing.T) {
	require := require.New(t)
	vm, _, _ := defaultVM(t, cortina)
	vm.ctx.Lock.Lock()
	defer vm.ctx.Lock.Unlock()

	nodeID := ids.GenerateTestNodeID()
	require.NoError(vm.Connected(context.Background(), nodeID, version.CurrentApp))

	protocolAppRequestBytest, err := gossip.MarshalAppRequest(
		bloom.EmptyFilter.Marshal(),
		ids.Empty[:],
	)
	require.NoError(err)

	appRequestBytes := p2p.PrefixMessage(
		p2p.ProtocolPrefix(network.TxGossipHandlerID),
		protocolAppRequestBytest,
	)

	var (
		eg          errgroup.Group
		ctx, cancel = context.WithCancel(context.Background())
	)
	// keep 10 workers running
	for i := 0; i < 10; i++ {
		eg.Go(func() error {
			for ctx.Err() == nil {
				err := vm.AppRequest(
					context.Background(),
					nodeID,
					0,
					time.Now().Add(time.Hour),
					appRequestBytes,
				)
				if err != nil {
					return err
				}
			}
			return nil
		})
	}

	// If the validator set lock isn't held, the race detector should fail here.
	for i := uint64(0); i < 1000; i++ {
		blk, err := block.NewBanffStandardBlock(
			time.Now(),
			vm.state.GetLastAccepted(),
			i,
			nil,
		)
		require.NoError(err)

		vm.state.SetLastAccepted(blk.ID())
		vm.state.SetHeight(blk.Height())
		vm.state.AddStatelessBlock(blk)
	}

	// If the validator set lock is grabbed, we need to make sure to release the
	// lock to avoid a deadlock.
	vm.ctx.Lock.Unlock()
	cancel() // stop and wait for workers
	require.NoError(eg.Wait())
	vm.ctx.Lock.Lock()
}

func buildAndAcceptStandardBlock(vm *VM) error {
	blk, err := vm.Builder.BuildBlock(context.Background())
	if err != nil {
		return err
	}

	if err := blk.Verify(context.Background()); err != nil {
		return err
	}

	if err := blk.Accept(context.Background()); err != nil {
		return err
	}

	return vm.SetPreference(context.Background(), vm.manager.LastAccepted())
}

func checkValidatorBlsKeyIsSet(
	valState validators.State,
	nodeID ids.NodeID,
	subnetID ids.ID,
	height uint64,
	expectedBlsKey *bls.PublicKey,
) error {
	vals, err := valState.GetValidatorSet(context.Background(), height, subnetID)
	if err != nil {
		return err
	}

	val, found := vals[nodeID]
	switch {
	case !found:
		return database.ErrNotFound
	case expectedBlsKey == val.PublicKey:
		return nil
	case expectedBlsKey == nil && val.PublicKey != nil:
		return errors.New("unexpected BLS key")
	case expectedBlsKey != nil && val.PublicKey == nil:
		return errors.New("missing BLS key")
	case !bytes.Equal(bls.SerializePublicKey(expectedBlsKey), bls.SerializePublicKey(val.PublicKey)):
		return errors.New("incorrect BLS key")
	default:
		return nil
	}
}
