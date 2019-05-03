package pf

import (
	"fmt"
	"math"

	filter "github.com/marco-hrlic/go-estimate"
	"github.com/marco-hrlic/go-estimate/estimate"
	"github.com/marco-hrlic/go-estimate/noise"
	"github.com/marco-hrlic/go-estimate/rand"
	"github.com/milosgajdos83/matrix"
	"gonum.org/v1/gonum/floats"
	"gonum.org/v1/gonum/mat"
	"gonum.org/v1/gonum/stat/distmv"
)

// PF is a Bootstrap Filter a.k.a. SIR Particle Filter.
// For more information about Bootstrap Filter see:
// https://en.wikipedia.org/wiki/Particle_filter#The_bootstrap_filter
type PF struct {
	// model is bootstrap filter model
	model filter.Model
	// w stores particle weights
	w []float64
	// x stores filter particles as column vectors
	x *mat.Dense
	// y stores particle outputs
	y *mat.Dense
	// q is state noise a.k.a. process noise
	q filter.Noise
	// r is output noise a.k.a. measurement noise
	r filter.Noise
	// inn stores a diff between measurement vector and particular particle output.
	// In Kalman filter family similar vector is referred to as "innovation vector".
	// The size of inn is fixed -- it's equal to the size of the system output,
	// so we preallocate it to avoid reallocating it on every call to Update().
	inn []float64
	// errPDF is PDF (Probability Density Function) of filter output error
	errPDF distmv.RandLogProber
}

// New creates new Particle Filter (PF) with the following parameters and returns it:
// - m:     system model
// - init:  initial condition of the filter
// - q:     state  noise a.k.a. process noise
// - r:     output  noise a.k.a. measurement noise
// - p:     number of filter particles
// - pdf:   Probability Density Function (PDF) of filter output error
// New returns error if non-positive number of particles is given or if the particles fail to be generated.
func New(m filter.Model, ic filter.InitCond, q, r filter.Noise, p int, pdf distmv.RandLogProber) (*PF, error) {
	// must have at least one particle; can't be negative
	if p <= 0 {
		return nil, fmt.Errorf("Invalid particle count: %d", p)
	}

	// size of input and output vectors
	in, out := m.Dims()
	if in <= 0 || out <= 0 {
		return nil, fmt.Errorf("Invalid model dimensions: [%d x %d]", in, out)
	}

	if q != nil {
		if q.Cov().Symmetric() != in {
			return nil, fmt.Errorf("Invalid state noise dimension: %d", q.Cov().Symmetric())
		}
	} else {
		q, _ = noise.NewZero(in)
	}

	if r != nil {
		if r.Cov().Symmetric() != out {
			return nil, fmt.Errorf("Invalid output noise dimension: %d", r.Cov().Symmetric())
		}
	} else {
		r, _ = noise.NewZero(out)
	}

	// Initialize particle weights to equal probabilities:
	// particle weights must sum up to 1 to represent probability
	w := make([]float64, p)
	for i := range w {
		w[i] = 1 / float64(p)
	}

	// draw particles from distribution with covariance InitCond.Cov()
	x, err := rand.WithCovN(ic.Cov(), p)
	if err != nil {
		return nil, fmt.Errorf("Failed to generate filter particles: %v", err)
	}

	rows, cols := x.Dims()
	// center particles around initial state condition init.State()
	for c := 0; c < cols; c++ {
		for r := 0; r < rows; r++ {
			x.Set(r, c, x.At(r, c)+ic.State().AtVec(r))
		}
	}

	y := mat.NewDense(out, p, nil)
	inn := make([]float64, out)

	return &PF{
		model:  m,
		w:      w,
		x:      x,
		y:      y,
		q:      q,
		r:      r,
		inn:    inn,
		errPDF: pdf,
	}, nil
}

// New creates new Particle Filter (PF) with the following parameters and returns it:
// - m:     system model
// - q:     state  noise a.k.a. process noise
// - r:     output  noise a.k.a. measurement noise
// - p:     number of filter particles
// - pdf:   Probability Density Function (PDF) of filter output error
// - pd:    particle distribution
// New returns error if non-positive number of particles is given or if the particles fail to be generated.
func NewWithParticleDist(m filter.Model, q, r filter.Noise, p int, pdf distmv.RandLogProber, pd distmv.Rander) (*PF, error) {
	// must have at least one particle; can't be negative
	if p <= 0 {
		return nil, fmt.Errorf("Invalid particle count: %d", p)
	}

	// size of input and output vectors
	in, out := m.Dims()
	if in <= 0 || out <= 0 {
		return nil, fmt.Errorf("Invalid model dimensions: [%d x %d]", in, out)
	}

	if q != nil {
		if q.Cov().Symmetric() != in {
			return nil, fmt.Errorf("Invalid state noise dimension: %d", q.Cov().Symmetric())
		}
	} else {
		q, _ = noise.NewZero(in)
	}

	if r != nil {
		if r.Cov().Symmetric() != out {
			return nil, fmt.Errorf("Invalid output noise dimension: %d", r.Cov().Symmetric())
		}
	} else {
		r, _ = noise.NewZero(out)
	}

	// Initialize particle weights to equal probabilities:
	// particle weights must sum up to 1 to represent probability
	w := make([]float64, p)
	x := mat.NewDense(in, p, nil)
	for i := range w {
		w[i] = 1 / float64(p)
        x.SetCol(i, pd.Rand(nil))
	}

	y := mat.NewDense(out, p, nil)
	inn := make([]float64, out)

	return &PF{
		model:  m,
		w:      w,
		x:      x,
		y:      y,
		q:      q,
		r:      r,
		inn:    inn,
		errPDF: pdf,
	}, nil
}

// Predict estimates the next system state and its output given the state x and input u and returns it.
// Predict modifies internal state of the filter: it updates its particle with their predicted values.
// It returns error if it fails to propagate either the filter particles or x to the next state.
func (b *PF) Predict(x, u mat.Vector) (filter.Estimate, error) {
	// propagate input state to the next step
	xNext, err := b.model.Propagate(x, u, b.q.Sample())
	if err != nil {
		return nil, fmt.Errorf("System state propagation failed: %v", err)
	}

	r, c := b.x.Dims()
	xPred := mat.NewDense(r, c, nil)

	// propagate filter particles to the next step
	for c := range b.w {
		xPartNext, err := b.model.Propagate(b.x.ColView(c), u, b.q.Sample())
		if err != nil {
			return nil, fmt.Errorf("Particle state propagation failed: %v", err)
		}
		xPred.Slice(0, xPartNext.Len(), c, c+1).(*mat.Dense).Copy(xPartNext)
	}

	// update filter particles and their observed outputs
	b.x.Copy(xPred)

	return estimate.NewBase(xNext)
}

// Update corrects state x using the measurement z given control intput u and returns the corrected estimate.
// It returns error if it fails to calculate system output estimate or if the size of z is invalid.
func (b *PF) Update(x, w, u, z mat.Vector) (filter.Estimate, []float64, error) {
	if z.Len() != len(b.inn) {
		return nil, fmt.Errorf("Invalid measurement size: %d", z.Len())
	}

	r, c := b.y.Dims()
	yPred := mat.NewDense(r, c, nil)

	// observe system output for each particle
	for c := range b.w {
		yPart, err := b.model.Observe(b.x.ColView(c), u, b.r.Sample())
		if err != nil {
			return nil, nil, fmt.Errorf("Particle state observation failed: %v", err)
		}
		yPred.Slice(0, yPart.Len(), c, c+1).(*mat.Dense).Copy(yPart)
	}

	// Update particle weights:
	for c := range w {
        var PL_d float64 = x.AtVec(1) + 10 * x.AtVec(0) * math.Log(z.AtVec(0)) + b.errPDF.Rand(nil)[0]
        tmp := []float64{z.AtVec(1) - PL_d}
		// turn the innovation vector i.e. measurement error into probability
		// Note: this isn't actually probability but that's ok because we normalize weights
		diff := math.Exp(b.errPDF.LogProb(tmp))
		w[c] = w[c] * diff
	}

	// normalize the particle weights so they express probability
	floats.Scale(1/floats.Sum(w), w)

	rows, _ := b.x.Dims()
	wavg := 0.0
	// FIXME: probably no need to allocate a new vector here - just modify b.x
	xEst := mat.NewVecDense(rows, nil)
	// update (correct) particles estimates to weighted average
	for r := 0; r < rows; r++ {
		for c := range w {
			wavg += w[c] * x.At(r, c)
		}
		xEst.SetVec(r, wavg)
		wavg = 0.0
	}

	// update filter particle outputs
	b.y.Copy(yPred)

	return estimate.NewBase(xEst), w, nil
}

// Run runs one step of Bootstrap Filter for given state x, input u and measurement z.
// It corrects system state estimate x using measurement z and returns a new state estimate.
// It returns error if it either fails to propagate particles or update the state x.
func (b *PF) Run(x, u, z mat.Vector) (filter.Estimate, error) {
	pred, err := b.Predict(x, u)
	if err != nil {
		return nil, err
	}

	est, err := b.Update(pred.Val(), u, z)
	if err != nil {
		return nil, err
	}

	return est, nil
}

// Resample allows to resample filter particles with regularization parameter alpha.
// It generates new filter particles and replaces the existing ones with them.
// If invalid (non-positive) alpha is provided we use optimal alpha for gaussian kernel.
// It returns error if it fails to generate new filter particles.
func (b *PF) Resample(alpha float64) error {
	// randomly pick new particles based on their weights
	// rand.RouletteDrawN returns a slice of column indices to b.x
	indices, err := rand.RouletteDrawN(b.w, len(b.w))
	if err != nil {
		return fmt.Errorf("Failed to sample filter particles: %v", err)
	}

	// we need to clone b.x to avoid overriding the existing filter particles
	x := new(mat.Dense)
	x.Clone(b.x)
	rows, cols := x.Dims()

	// length of inidices slice is the same as number of columns: number of particles
	for c := range indices {
		b.x.Slice(0, rows, c, c+1).(*mat.Dense).Copy(x.ColView(indices[c]))
	}

	// we have resampled particles, therefore we must reinitialize their weights, too:
	// weights will have the same probability: 1/len(b.w): they must sum up to 1
	for i := 0; i < len(b.w); i++ {
		b.w[i] = 1 / float64(len(b.w))
	}

	// We need to calculate covariance matrix of particles
	cov, err := matrix.Cov(b.x, "cols")
	if err != nil {
		return fmt.Errorf("Failed to calculate covariance matrix: %v", err)
	}

	// randomly draw values with given particle covariance
	m, err := rand.WithCovN(cov, cols)
	if err != nil {
		return fmt.Errorf("Failed to draw random particle pertrubations: %v", err)
	}

	// if invalid alpha is given, use the optimal value for Gaussian
	if alpha <= 0 {
		alpha = AlphaGauss(rows, cols)
	}

	m.Scale(alpha, m)

	// add random perturbations to the new particles
	b.x.Add(b.x, m)

	return nil
}


// Particles returns PF particles
func (b *PF) Particles() mat.Matrix {
	p := &mat.Dense{}
	p.Clone(b.x)

	return p
}

// Weights returns a vector containing PF particle weights
func (b *PF) Weights() mat.Vector {
	data := make([]float64, len(b.w))
	copy(data, b.w)

	return mat.NewVecDense(len(data), data)
}

// AlphaGauss computes optimal regulariation parameter for Gaussian kernel and returns it.
func AlphaGauss(r, c int) float64 {
	return math.Pow(4.0/(float64(c)*(float64(r)+2.0)), 1/(float64(r)+4.0))
}