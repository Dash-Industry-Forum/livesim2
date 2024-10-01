package app

// GCDuint32 - greatest common divisor (GCD) via Euclidean algorithm
func GCDuint32(a, b uint32) uint32 {
	for b != 0 {
		t := b
		b = a % b
		a = t
	}
	return a
}
