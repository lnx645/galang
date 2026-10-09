package interp

import (
	"galang/internal/domain"
)

// SetProgramArgs exposes command line arguments to the program as the global
// array `args`.
func (in *Interp) SetProgramArgs(args []string) {
	items := make([]domain.Value, 0, len(args))
	for _, a := range args {
		items = append(items, domain.Str(a))
	}
	in.gscope.names["args"] = &cslot{kind: slotVal, idx: in.gscope.layout.addVal()}
	in.globals.grow(in.gscope.layout)
	in.globals.vals[in.gscope.names["args"].idx] = &domain.Arr{Items: items}
}
