package speech

import "testing"

func TestLimiterEnforcesPerUserAndGlobalLimits(t *testing.T) {
	limiter, err := NewLimiter(2, 1)
	if err != nil {
		t.Fatalf("NewLimiter: %v", err)
	}

	releaseAlice, ok := limiter.Acquire("alice")
	if !ok {
		t.Fatal("first alice acquire was rejected")
	}
	if _, ok := limiter.Acquire("alice"); ok {
		t.Fatal("second alice acquire bypassed the per-user limit")
	}

	releaseBob, ok := limiter.Acquire("bob")
	if !ok {
		t.Fatal("first bob acquire was rejected")
	}
	if _, ok := limiter.Acquire("carol"); ok {
		t.Fatal("carol acquire bypassed the global limit")
	}

	releaseAlice()
	releaseAlice() // Release must be idempotent.
	if releaseCarol, ok := limiter.Acquire("carol"); !ok {
		t.Fatal("global slot was not released")
	} else {
		releaseCarol()
	}
	releaseBob()
}

func TestNewLimiterRejectsInvalidLimits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		global  int
		perUser int
	}{
		{name: "zero global", global: 0, perUser: 1},
		{name: "zero per-user", global: 1, perUser: 0},
		{name: "per-user exceeds global", global: 1, perUser: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewLimiter(tc.global, tc.perUser); err == nil {
				t.Fatal("NewLimiter returned nil error")
			}
		})
	}
}
