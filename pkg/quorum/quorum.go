// Package quorum provides the quorum size calculation per the Raft spec.
// Q = floor(N/2) + 1
package quorum

// Size returns the minimum number of voters required for a quorum in a
// cluster of n voters. If n == 0, returns 1 to avoid divide-by-zero
// callers issuing vacuous decisions.
func Size(n int) int {
	if n <= 0 {
		return 1
	}
	return (n / 2) + 1
}

// HasQuorum reports whether the number of reachable voters satisfies quorum
// for a cluster of total voters.
func HasQuorum(total, reachable int) bool {
	return reachable >= Size(total)
}
