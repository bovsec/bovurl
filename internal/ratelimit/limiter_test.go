package ratelimit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLimiter_Unlimited(t *testing.T) {
	l := New(0)
	ctx := context.Background()
	start := time.Now()
	for i := 0; i < 100; i++ {
		if err := l.Wait(ctx); err != nil {
			t.Fatalf("beklenmeyen hata: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("sinirsiz limiter cok yavas calisti: %v", elapsed)
	}
}

func TestLimiter_RespectsRate(t *testing.T) {
	// saniyede 10 istek -> her istek arasi ~100ms
	l := New(10)
	ctx := context.Background()

	start := time.Now()
	for i := 0; i < 5; i++ {
		if err := l.Wait(ctx); err != nil {
			t.Fatalf("beklenmeyen hata: %v", err)
		}
	}
	elapsed := time.Since(start)

	// 5 istek, ilki aninda gecer, sonraki 4'u ~100ms araliklarla ->
	// toplamda en az ~350ms beklenir (jitter/sistem gecikmesi icin toleransli).
	minExpected := 350 * time.Millisecond
	if elapsed < minExpected {
		t.Fatalf("rate limit yeterince yavaslatmadi: %v (beklenen en az %v)", elapsed, minExpected)
	}
}

func TestLimiter_ContextCancellation(t *testing.T) {
	l := New(1) // saniyede 1 istek - yavas
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	// ilk cagri hemen gecer
	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("ilk cagri hata vermemeliydi: %v", err)
	}
	// ikinci cagri ~1 saniye beklemeli ama context 10ms'de iptal olacak
	err := l.Wait(ctx)
	if err == nil {
		t.Fatal("context iptal olduktan sonra hata beklenirdi")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("context.DeadlineExceeded bekleniyordu, alinan: %v", err)
	}
}

func TestWithBackoff_SucceedsEventually(t *testing.T) {
	attempts := 0
	err := WithBackoff(context.Background(), 3, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("gecici hata")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("3. denemede basarili olmaliydi: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("3 deneme bekleniyordu, alinan: %d", attempts)
	}
}

func TestWithBackoff_ExhaustsRetries(t *testing.T) {
	attempts := 0
	wantErr := errors.New("kalici hata")
	err := WithBackoff(context.Background(), 2, func() error {
		attempts++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("kalici hata beklenirdi, alinan: %v", err)
	}
	if attempts != 3 { // ilk deneme + 2 retry
		t.Fatalf("3 toplam deneme bekleniyordu (1+2 retry), alinan: %d", attempts)
	}
}
