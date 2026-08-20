// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package ipam

import "github.com/cilium/cilium/pkg/node"

func localInfo(n node.Node) node.LocalNodeInfo {
	info, local := n.Local()
	if !local {
		panic("test node is not local")
	}
	return info
}
