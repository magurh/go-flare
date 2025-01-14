package snowball

import (
	"encoding/csv"
	"os"
	"strconv"
	"testing"

	"fmt"

	"github.com/ava-labs/avalanchego/utils/sampler"
)

// A test for fully a honest network
func TestHonestSnowballTreeVsFlat(t *testing.T) {
	numColors := 1
	numNodes := 100
	params := Parameters{
		K: 20, Alpha: 15, BetaVirtuous: 15, BetaRogue: 20,
	}
	seed := int64(0)
	N := 1000 // Number of simulations

	// Open CSV file for writing results
	file, err := os.Create("consensus_performance.csv")
	if err != nil {
		t.Fatalf("Failed to create CSV file: %v", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write CSV header
	writer.Write([]string{"Run", "NumRoundsTree", "NumRoundsFlat"})

	// Run the simulation N times
	for run := 1; run <= N; run++ {
		nBitwise := Network{}
		nBitwise.Initialize(params, numColors)

		nNaive := nBitwise 

		sampler.Seed(seed)
		// Tree implementation for all nodes
		for i := 0; i < numNodes; i++ {
			nBitwise.AddNode(&Tree{})
		}

		sampler.Seed(seed)
		// Flat implementation for all nodes
		for i := 0; i < numNodes; i++ {
			nNaive.AddNode(&Flat{})
		}

		numRoundsTree := 0
		numRoundsFlat := 0

		for {
			if !nBitwise.Finalized() {
				sampler.Seed(int64(numRoundsTree) + seed)
				nBitwise.Round()
				numRoundsTree++
			}

			if !nNaive.Finalized() {
				sampler.Seed(int64(numRoundsFlat) + seed)
				nNaive.Round()
				numRoundsFlat++
			}

			// Break the loop if both implementations have finalized
			if nBitwise.Finalized() && nNaive.Finalized() {
				break
			}

			// Check for disagreement and handle errors
			if nBitwise.Disagreement() || nNaive.Disagreement() {
				t.Fatalf("Network agreed on inconsistent values")
			}
		}

		// Ensure both implementations reached agreement
		if !nNaive.Agreement() {
			t.Fatalf("Flat network failed to reach agreement")
		}
		if !nBitwise.Agreement() {
			t.Fatalf("Tree network failed to reach agreement")
		}

		// Write the results to the CSV file
		writer.Write([]string{
			strconv.Itoa(run),
			strconv.Itoa(numRoundsTree),
			strconv.Itoa(numRoundsFlat),
		})
	}
}


// Testing either Flat or Tree implementation
// for multiple sets of parameters
func TestMultipleHonestSnowballParams(t *testing.T) {
	numColors := 2
	numNodes := 100
	seed := int64(0)
	N := 1000 // Number of simulations

	// Define multiple parameter sets
	paramSets := []Parameters{
		{K: 10, Alpha: 6, BetaVirtuous: 10, BetaRogue: 13},
		{K: 10, Alpha: 7, BetaVirtuous: 10, BetaRogue: 13},
		{K: 10, Alpha: 8, BetaVirtuous: 10, BetaRogue: 13},
		{K: 20, Alpha: 11, BetaVirtuous: 10, BetaRogue: 13},
		{K: 20, Alpha: 12, BetaVirtuous: 10, BetaRogue: 13},
		{K: 20, Alpha: 13, BetaVirtuous: 10, BetaRogue: 13},
		{K: 20, Alpha: 14, BetaVirtuous: 10, BetaRogue: 13},
		{K: 20, Alpha: 15, BetaVirtuous: 10, BetaRogue: 13},
	}

	// Open CSV file for writing results
	file, err := os.Create("consensus_performance.csv")
	if err != nil {
		t.Fatalf("Failed to create CSV file: %v", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write CSV header
	header := []string{"Run"}
	for i := range paramSets {
		header = append(header, fmt.Sprintf("NumRounds%d", i+1))
	}
	writer.Write(header)

	// Run the simulation N times
	for run := 1; run <= N; run++ {
		// To store the number of rounds for each parameter set in this run
		numRoundsForParams := make([]string, len(paramSets))

		// Iterate over each parameter set
		for paramIndex, params := range paramSets {
			nBitwise := Network{}
			nBitwise.Initialize(params, numColors)

			sampler.Seed(seed)
			// Tree implementation for all nodes
			for i := 0; i < numNodes; i++ {
				nBitwise.AddNode(&Tree{}) // or Flat
			}

			numRounds := 0

			// Run the rounds until finalization
			for !nBitwise.Finalized() {
				sampler.Seed(int64(numRounds) + seed)
				nBitwise.Round()
				numRounds++
			}

			// Store the result for this parameter set
			numRoundsForParams[paramIndex] = strconv.Itoa(numRounds)
		}

		// Write a single row with "Run" and all numRounds results
		row := append([]string{strconv.Itoa(run)}, numRoundsForParams...)
		writer.Write(row)
	}
}

