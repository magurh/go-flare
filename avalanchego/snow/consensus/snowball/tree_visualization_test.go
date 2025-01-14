package snowball_test

import (
    "fmt"
    "strings"
    "testing"

    "github.com/ava-labs/avalanchego/ids"
    "github.com/ava-labs/avalanchego/snow/consensus/snowball"
)

func idToBinary(id ids.ID) string {
    bytes := id[:]
    var binaryStrings []string
    for _, b := range bytes {
        binaryStrings = append(binaryStrings, fmt.Sprintf("%08b", b))
    }
    return strings.Join(binaryStrings, "")
}

func TestTreeStructureDummy(t *testing.T) {
    // Manually binary set ids of decisions to be added
	zero := ids.ID{0b00000000}
	one := ids.ID{0b00000001}
	two := ids.ID{0b00000010}
	four := ids.ID{0b00000100}

	params := snowball.Parameters{
		K: 1, Alpha: 1, BetaVirtuous: 1, BetaRogue: 2,
	}
	tree := snowball.Tree{}
	tree.Initialize(params, zero)
    tree.Add(two)
	tree.Add(one)
	tree.Add(four)

    children := tree.AllChildren()
    for addr, child := range children {
        fmt.Printf("Node: %s, Parent Node: %s, DecidedPrefix: %d, CommonPrefix: %d,  Preference: %s\n",
            addr,
            child.ParentAddress,
            child.DecidedPrefix,
            child.CommonPrefix,
            idToBinary(child.Preference)[:8],
        )
        fmt.Printf(child.Confidence)
        fmt.Printf("\n")
    }

}


func TestTreeStructureRandom(t *testing.T) {
    params := snowball.Parameters{
        K:            5,
        Alpha:        3,
        BetaVirtuous: 2,
        BetaRogue:    4,
    }
    // Initialize tree
    tree := snowball.Tree{}
    initialPreference := ids.GenerateTestID()
    tree.Initialize(params, initialPreference)

    fmt.Printf("Initial decision: %s  Binary: %s\n", initialPreference.String()[:8], idToBinary(initialPreference)[:8])

    // Add decisions
    for i := 0; i < 3; i++ {
        newID := ids.GenerateTestID()
        tree.Add(newID)
        fmt.Printf("Added Decision: %s Binary: %s\n", newID.String()[:8], idToBinary(newID)[:8])
    }
    fmt.Println()

    // Print children
    children := tree.AllChildren()
    for addr, child := range children {
        fmt.Printf("Node: %s, Parent Node: %s, DecidedPrefix: %d, CommonPrefix: %d, Preference: %s\n",
            addr,
            child.ParentAddress,
            child.DecidedPrefix,
            child.CommonPrefix,
            idToBinary(child.Preference)[:8],
        )
        fmt.Printf(child.Confidence)
        fmt.Printf("\n")
    }
}

// The following three functions correspond to 3 distinct scenarios
func TestVisualizeChangingTree1(t *testing.T) {
    params := snowball.Parameters{
        K:            5,
        Alpha:        3,
        BetaVirtuous: 2,
        BetaRogue:    3,
    }
    tree := snowball.Tree{}
    initialPreference := ids.GenerateTestID()
    tree.Initialize(params, initialPreference)

    var decisionIDs []ids.ID
    decisionIDs = append(decisionIDs, initialPreference)
    fmt.Printf("Initial decision: %s  Binary: %s\n", initialPreference.String()[:8], idToBinary(initialPreference)[:8])

    // Add decisions
    for i := 0; i < 3; i++ {
        newID := ids.GenerateTestID()
        decisionIDs = append(decisionIDs, newID)
        tree.Add(newID)
        fmt.Printf("Added Decision: %s Binary: %s\n", newID.String()[:8], idToBinary(newID)[:8])
    }
    fmt.Println()

    // Print initial tree structure
    children := tree.AllChildren()
    for addr, child := range children {
        fmt.Printf("Node: %s, Parent Node: %s, DecidedPrefix: %d, CommonPrefix: %d,  Preference: %s\n",
            addr,
            child.ParentAddress,
            child.DecidedPrefix,
            child.CommonPrefix,
            idToBinary(child.Preference)[:8],
        )
        fmt.Printf(child.Confidence)
        fmt.Printf("\n")
    }
    fmt.Println()

    // Create polls
        polls := []ids.Bag{
        func() ids.Bag {
            b := ids.Bag{}
            b.AddCount(decisionIDs[1], 3) // majority for ID[1]
            b.AddCount(decisionIDs[2], 2)
            return b
        }(),
        func() ids.Bag {
            b := ids.Bag{}
            // This poll is split
            b.AddCount(decisionIDs[0], 1)
            b.AddCount(decisionIDs[1], 1)
            b.AddCount(decisionIDs[2], 1)
            return b
        }(),
        func() ids.Bag {
            b := ids.Bag{}
            // Strong majority for ID[3]
            b.AddCount(decisionIDs[3], 4)
            return b
        }(),
        func() ids.Bag {
            b := ids.Bag{}
            // Slight majority for ID[3], re-affirming ID[3]
            b.AddCount(decisionIDs[3], 4)
            b.AddCount(decisionIDs[2], 1)
            return b
        }(),
        func() ids.Bag {
            b := ids.Bag{}
            // Slight majority for ID[3], re-affirming ID[3]
            b.AddCount(decisionIDs[3], 3)
            b.AddCount(decisionIDs[0], 1)
            b.AddCount(decisionIDs[2], 1)
            return b
        }(),
    }

    // Simulate polling rounds
    for round, poll := range polls {
        fmt.Printf("Round %d:\n", round+1)
        for _, id := range poll.List() {
            fmt.Printf("Polling result -> %s : %d votes\n", idToBinary(id)[:8], poll.Count(id))
        }
        // Record the poll in the tree
        tree.RecordPoll(poll)
        
        // Print the current top-level Preference after this round
        pref := idToBinary(tree.Preference())
        fmt.Printf("After Round %d: Current Preference = %s\n", round+1, pref[:8])

        // After each poll round:
         children := tree.AllChildren()
        for addr, child := range children {
            fmt.Printf("Node: %s, Parent Node: %s, DecidedPrefix: %d, CommonPrefix: %d, Preference: %s\n",
                addr,
                child.ParentAddress,
                child.DecidedPrefix,
                child.CommonPrefix,
                idToBinary(child.Preference)[:8],
            )
            fmt.Printf(child.Confidence)
            fmt.Printf("\n")
        }
        fmt.Println()

        // Check if the Tree has become finalized.
        if tree.Finalized() {
            fmt.Printf("Tree finalized on preference: %s\nBinary: %s\n\n",
                tree.Preference(), idToBinary(tree.Preference())[:8])
            break
        }
    }

    // Final check if we ended without finalization.
    if !tree.Finalized() {
        fmt.Println("Tree is not yet finalized after all rounds.")
        fmt.Printf("Current Preference: %s\n\n", idToBinary(tree.Preference())[:8])
    }
}


func TestVisualizeChangingTree2(t *testing.T) {
    params := snowball.Parameters{
        K:            5,
        Alpha:        3,
        BetaVirtuous: 4,
        BetaRogue:    5,
    }
    tree := snowball.Tree{}
    initialPreference := ids.GenerateTestID()
    tree.Initialize(params, initialPreference)

    var decisionIDs []ids.ID
    decisionIDs = append(decisionIDs, initialPreference)
    fmt.Printf("Initial decision: %s  Binary: %s\n", initialPreference.String()[:8], idToBinary(initialPreference)[:8])

    // Add decisions
    for i := 0; i < 3; i++ {
        newID := ids.GenerateTestID()
        decisionIDs = append(decisionIDs, newID)
        tree.Add(newID)
        fmt.Printf("Added Decision: %s Binary: %s\n", newID.String()[:8], idToBinary(newID)[:8])
    }
    fmt.Println()

    // Print initial tree structure
    children := tree.AllChildren()
    for addr, child := range children {
        fmt.Printf("Node: %s, Parent Node: %s, DecidedPrefix: %d, CommonPrefix: %d,  Preference: %s\n",
            addr,
            child.ParentAddress,
            child.DecidedPrefix,
            child.CommonPrefix,
            idToBinary(child.Preference)[:8],
        )
        fmt.Printf(child.Confidence)
        fmt.Printf("\n")
    }
    fmt.Println()

    // Create polls
        polls := []ids.Bag{
        func() ids.Bag {
            b := ids.Bag{}
            b.AddCount(decisionIDs[1], 5) // majority for ID[1]
            return b
        }(),
        func() ids.Bag {
            b := ids.Bag{}
            b.AddCount(decisionIDs[1], 5) // majority for ID[1]
            return b
        }(),
        func() ids.Bag {
            b := ids.Bag{}
            b.AddCount(decisionIDs[1], 5) // majority for ID[1]
            return b
        }(),
        func() ids.Bag {
            b := ids.Bag{}
            b.AddCount(decisionIDs[1], 5) // majority for ID[1]
            return b
        }(),
        func() ids.Bag {
            b := ids.Bag{}
            // Strong majority for ID[3]
            b.AddCount(decisionIDs[3], 4)
            return b
        }(),
        func() ids.Bag {
            b := ids.Bag{}
            // Slight majority for ID[1]
            b.AddCount(decisionIDs[1], 4)
            b.AddCount(decisionIDs[2], 1)
            return b
        }(),
    }

    // Simulate polling rounds
    for round, poll := range polls {
        fmt.Printf("Round %d:\n", round+1)
        for _, id := range poll.List() {
            fmt.Printf("Polling result -> %s : %d votes\n", idToBinary(id)[:8], poll.Count(id))
        }
        // Record the poll in the tree
        tree.RecordPoll(poll)
        
        // Print the current top-level Preference after this round
        pref := idToBinary(tree.Preference())
        fmt.Printf("After Round %d: Current Preference = %s\n", round+1, pref[:8])

        // After each poll round:
         children := tree.AllChildren()
        for addr, child := range children {
            fmt.Printf("Node: %s, Parent Node: %s, DecidedPrefix: %d, CommonPrefix: %d, Preference: %s\n",
                addr,
                child.ParentAddress,
                child.DecidedPrefix,
                child.CommonPrefix,
                idToBinary(child.Preference)[:8],
            )
            fmt.Printf(child.Confidence)
            fmt.Printf("\n")
        }
        fmt.Println()

        // Check if the Tree has become finalized.
        if tree.Finalized() {
            fmt.Printf("Tree finalized on preference: %s\nBinary: %s\n\n",
                tree.Preference(), idToBinary(tree.Preference())[:8])
            break
        }
    }

    // Final check if we ended without finalization.
    if !tree.Finalized() {
        fmt.Println("Tree is not yet finalized after all rounds.")
        fmt.Printf("Current Preference: %s\n\n", idToBinary(tree.Preference())[:8])
    }
}


func TestVisualizeChangingTree3(t *testing.T) {
    params := snowball.Parameters{
        K:            5,
        Alpha:        3,
        BetaVirtuous: 4,
        BetaRogue:    5,
    }
    tree := snowball.Tree{}
    zero := ids.ID{0b00000000}
	one := ids.ID{0b00000001}
	two := ids.ID{0b00000010}
	three := ids.ID{0b00000011}

	tree.Initialize(params, zero)
    tree.Add(two)
	tree.Add(one)
	tree.Add(three)

    decisionIDs := []ids.ID{zero, one, two, three}

    // Print initial tree structure
    children := tree.AllChildren()
    for addr, child := range children {
        fmt.Printf("Node: %s, Parent Node: %s, DecidedPrefix: %d, CommonPrefix: %d,  Preference: %s\n",
            addr,
            child.ParentAddress,
            child.DecidedPrefix,
            child.CommonPrefix,
            idToBinary(child.Preference)[:8],
        )
        fmt.Printf(child.Confidence)
        fmt.Printf("\n")
    }
    fmt.Println()

    // Create polls
        polls := []ids.Bag{
        func() ids.Bag {
            b := ids.Bag{}
            b.AddCount(decisionIDs[1], 5) // majority for ID[1]
            return b
        }(),
        func() ids.Bag {
            b := ids.Bag{}
            b.AddCount(decisionIDs[1], 5) // majority for ID[1]
            return b
        }(),
        func() ids.Bag {
            b := ids.Bag{}
            b.AddCount(decisionIDs[1], 1) // undecided
            return b
        }(),
        func() ids.Bag {
            b := ids.Bag{}
            b.AddCount(decisionIDs[1], 5) // majority for ID[1]
            return b
        }(),
    }

    // Simulate polling rounds
    for round, poll := range polls {
        fmt.Printf("Round %d:\n", round+1)
        for _, id := range poll.List() {
            fmt.Printf("Polling result -> %s : %d votes\n", idToBinary(id)[:8], poll.Count(id))
        }
        // Record the poll in the tree
        tree.RecordPoll(poll)
        
        // Print the current top-level Preference after this round
        pref := idToBinary(tree.Preference())
        fmt.Printf("After Round %d: Current Preference = %s\n", round+1, pref[:8])

        // After each poll round:
         children := tree.AllChildren()
        for addr, child := range children {
            fmt.Printf("Node: %s, Parent Node: %s, DecidedPrefix: %d, CommonPrefix: %d, Preference: %s\n",
                addr,
                child.ParentAddress,
                child.DecidedPrefix,
                child.CommonPrefix,
                idToBinary(child.Preference)[:8],
            )
            fmt.Printf(child.Confidence)
            fmt.Printf("\n")
        }
        fmt.Println()

        // Check if the Tree has become finalized.
        if tree.Finalized() {
            fmt.Printf("Tree finalized on preference: %s\nBinary: %s\n\n",
                tree.Preference(), idToBinary(tree.Preference())[:8])
            break
        }
    }

    // Final check if we ended without finalization.
    if !tree.Finalized() {
        fmt.Println("Tree is not yet finalized after all rounds.")
        fmt.Printf("Current Preference: %s\n\n", idToBinary(tree.Preference())[:8])
    }
}
