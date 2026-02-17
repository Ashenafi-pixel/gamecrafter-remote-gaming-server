package crash

import (
	"crypto/rand"
	"math/big"
)

// StepMultiplier converts step (0, 1, 2, ...) to display multiplier: 1.00 + step*0.01
const StepSize = 0.01

// CrashStepMin/Max: crash happens at step in [Min, Max] (multiplier 1.10 to 5.00 for 10-400)
const CrashStepMin, CrashStepMax = 10, 400

// Multiplier returns the multiplier at step (e.g. step 50 -> 1.50)
func Multiplier(step int) float64 {
	if step < 0 {
		step = 0
	}
	return 1.0 + float64(step)*StepSize
}

// GenerateCrashStep returns a random crash step in [CrashStepMin, CrashStepMax] using CSPRNG.
func GenerateCrashStep() int {
	n := CrashStepMax - CrashStepMin + 1
	max := big.NewInt(int64(n))
	v, err := rand.Int(rand.Reader, max)
	if err != nil {
		return CrashStepMin
	}
	return CrashStepMin + int(v.Int64())
}
