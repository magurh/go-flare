// Copyright (C) 2019-2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package snowball

import (
	"fmt"

	"github.com/ava-labs/avalanchego/ids"
)


func (u *unaryNode) CommonPrefix() int { return u.commonPrefix }
func (u *unaryNode) GetChild() node { return u.child }


type TreeChildren struct {
    Node              node     // The actual node (unaryNode or binaryNode)
	ParentAddress     string
    DecidedPrefix     int      // The number of decided bits in the prefix
    CommonPrefix	  int      // The number of bits shared in the common prefix (for unaryNode)
    Preference        ids.ID   // The node's preferred choice
    Confidence        string   // The node's confidence and finalized status
}


// AllChildren traverses the entire Tree from the root,
// returning a map from "node's preference ID" to ThreeChildren.
func (t *Tree) AllChildren() map[string]TreeChildren {
    results := make(map[string]TreeChildren)
    if t.node == nil {
        return results
    }

    var traverse func(n node, parentAddr string)
    traverse = func(n node, parentAddr string) {
        if n == nil {
            return
        }

        // Use memory address as the key
        address := fmt.Sprintf("%p", n)

        if un, ok := n.(*unaryNode); ok {
            results[address] = TreeChildren{
                Node:          un,
                ParentAddress: parentAddr,
                DecidedPrefix: un.DecidedPrefix(),
                CommonPrefix:  un.CommonPrefix(),
                Preference:    un.Preference(),
                Confidence:    un.snowball.String(),
            }
            traverse(un.child, address)
        } else if bn, ok := n.(*binaryNode); ok {
            results[address] = TreeChildren{
                Node:          bn,
                ParentAddress: parentAddr,
                DecidedPrefix: bn.DecidedPrefix(),
                CommonPrefix:  -1, // binaryNode doesn't have CommonPrefix
                Preference:    bn.Preference(),
                Confidence:    bn.snowball.String(),
            }
            for _, child := range bn.children {
                traverse(child, address)
            }
        }
    }

    traverse(t.node, "")
    return results
}
