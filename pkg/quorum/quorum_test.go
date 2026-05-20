package quorum

import "testing"

func TestSize(t *testing.T) {
	cases := []struct{ n, want int }{
		{0, 0}, {1, 1}, {2, 2}, {3, 2}, {4, 3}, {5, 3}, {7, 4}, {9, 5},
	}
	for _, c := range cases {
		if got := Size(c.n); got != c.want {
			t.Errorf("Size(%d) = %d, want %d", c.n, got, c.want)
		}
	}
}

func TestTolerates(t *testing.T) {
	cases := []struct{ n, want int }{
		{1, 0}, {2, 0}, {3, 1}, {4, 1}, {5, 2}, {7, 3},
	}
	for _, c := range cases {
		if got := Tolerates(c.n); got != c.want {
			t.Errorf("Tolerates(%d) = %d, want %d", c.n, got, c.want)
		}
	}
}

func TestSatisfied(t *testing.T) {
	if !Satisfied(2, 3) {
		t.Errorf("Satisfied(2, 3) should be true")
	}
	if Satisfied(1, 3) {
		t.Errorf("Satisfied(1, 3) should be false")
	}
}
