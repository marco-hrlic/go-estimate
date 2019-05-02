package smooth

import filter "github.com/marco-hrlic/go-estimate"

// RauchTungStriebel is optimal filter smoother
type RTS interface {
	// filter.Smoother is filter smoother
	filter.Smoother
}
