package particle

import (
	filter "github.com/marco-hrlic/go-estimate"
	"gonum.org/v1/gonum/mat"
)

// Particle is Particle Filter
type Particle interface {
	// filter.Filter is dynamical system filter
	filter.Filter
	// Weights returns particle weights
	Weights() mat.Vector
}
