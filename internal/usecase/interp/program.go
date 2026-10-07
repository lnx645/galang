package interp

import (
	"garurda/internal/domain"
)

// SetProgramArgs exposes command line arguments to the program as the global
// array `args`.
func (in *Interp) SetProgramArgs(args []string) {
	items := make([]domain.Value, 0, len(args))
	for _, a := range args {
		items = append(items, domain.Str(a))
	}
	in.global.define("args", &domain.Arr{Items: items}, &domain.TypeExpr{Name: "array", Args: []*domain.TypeExpr{{Name: "string"}}})
}
