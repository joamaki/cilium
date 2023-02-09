package cell

import (
	"fmt"
	"strings"

	"github.com/cilium/cilium/pkg/hive/internal"
)

type d2State struct {
	out string
	// provides maps from object to the module providing it
	provides    map[string]string
	outs        map[string]bool
	connections map[string]bool
}

func (d *d2State) populateModules(cell Cell, modId string) {
	switch c := cell.(type) {
	case *module:
		for _, cell := range c.cells {
			d.populateModules(cell, c.id)
		}

	case group:
		for _, cell := range c {
			d.populateModules(cell, modId)
		}

	case *provider:
		for i := range c.ctors {
			info := c.infos[i]
			//ctor := internal.FuncName(c.ctors[i])
			for _, output := range info.Outputs {
				o := internal.TrimName(output.String())
				d.provides[o] = modId
				d.out += modId + "." + "\"" + o + "\"\n"
			}
		}

	case *invoker:

	default:
		// hackity hack hack
		x := fmt.Sprintf("%T", c)
		if strings.Contains(x, "Config") {
			x = internal.TrimName(x)
			x = strings.TrimPrefix(x, "*cell.config[")
			x = strings.TrimSuffix(x, "]")
			d.provides[x] = modId
			d.out += modId + "." + "\"" + x + "\"" + "\n"
		}
	}
}

func (d *d2State) populateEdges(cell Cell, modId string) {
	switch c := cell.(type) {
	case *module:
		for _, cell := range c.cells {
			d.populateEdges(cell, c.id)
		}
	case *provider:
		//d.out += modId + ": {\n"
		for i := range c.ctors {
			info := c.infos[i]
			//ctor := internal.FuncName(c.ctors[i])
			for _, input := range info.Inputs {
				in := internal.TrimName(input.String())
				in = strings.ReplaceAll(in, "[optional]", "")
				provider, ok := d.provides[in]
				if ok && provider != modId {
					arrow := fmt.Sprintf("%s -> %s.\"%s\"\n", modId, provider, in)
					if provider != "hive" && !d.connections[arrow] {
						d.out += arrow
						d.connections[arrow] = true
					}
				}
			}
			/*
				for _, output := range info.Outputs {
					o := internal.TrimName(output.String())
					arrow := fmt.Sprintf("%s.%s -> %s.\"%s\": { target-arrowhead: { shape: diamond } }\n", modId, ctor, modId, o)
					d.out += arrow

					//+ "." + "\"" + ctor + "\""
					//d.out += modId + "." + "\"" + o + "\"\n"
				}*/
		}
		//d.out += "}\n"
	}
}

func CreateD2(cells []Cell) string {
	d := d2State{provides: map[string]string{}, connections: map[string]bool{}}
	for _, c := range cells {
		d.populateModules(c, "")
	}

	// XXX fix this stuff. Perhaps make the hive defaults a module?
	d.provides["hive.Lifecycle"] = "hive"
	d.provides["logrus.FieldLogger"] = "hive"
	d.provides["hive.Shutdowner"] = "hive"
	d.provides["cell.StatusReporter"] = "hive"

	for _, c := range cells {
		d.populateEdges(c, "")
	}
	return d.out
}
