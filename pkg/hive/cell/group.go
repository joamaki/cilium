// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cell

// Group TBD
func Group(cells ...Cell) Cell {
	return group(cells)
}

type group []Cell

func (g group) Apply(c container) error {
	for _, cell := range g {
		if err := cell.Apply(c); err != nil {
			return err
		}
	}
	return nil
}

func (g group) Info(c container) Info {
	n := NewInfoNode("")
	for _, cell := range g {
		n.Add(cell.Info(c))
	}
	return n
}
