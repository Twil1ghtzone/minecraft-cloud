// Package quorum implements the canonical Raft quorum calculation used
// throughout AetherNet:
//
//	Q = floor(N/2) + 1
//
// The single source of truth lives here so that the panel UI, the
// installer, and the daemon all agree on whether a given membership
// constitutes a viable cluster.
package quorum

// Size returns Q for a cluster of N nodes.
func Size(n int) int {
	if n <= 0 {
		return 0
	}
	return n/2 + 1
}

// Tolerates returns the number of node failures a cluster of size n can
// survive while still committing writes.
func Tolerates(n int) int {
	return n - Size(n)
}

// Satisfied reports whether `alive` out of `total` nodes is enough to
// commit a write.
func Satisfied(alive, total int) bool {
	return alive >= Size(total)
}
