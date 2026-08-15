// Package ratelimit, harici bir kütüphaneye (örn. golang.org/x/time/rate) ihtiyaç
// duymadan basit bir token-bucket rate limiter sağlar. Her pasif kaynak (wayback,
// commoncrawl, alienvault, otx, urlscan) kendi Limiter örneğine sahip olmalı ki
// bir kaynağın yavaşlığı/kısıtlaması diğerini etkilemesin.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Limiter saniye başına belirli sayıda izin (token) veren basit bir yapı.
type Limiter struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
}

// New, saniyede en fazla `perSecond` istek izni veren bir limiter oluşturur.
// perSecond <= 0 ise limiter devre dışı kalır (sınırsız izin verir).
func New(perSecond int) *Limiter {
	if perSecond <= 0 {
		return &Limiter{interval: 0}
	}
	return &Limiter{interval: time.Second / time.Duration(perSecond)}
}

// Wait, bir sonraki isteğe izin verilene kadar bloklar veya context iptal
// edilirse hata döner. Basit ama thread-safe bir "sabit aralıkla izin ver"
// mantığı kullanır (jitter olmadan, MVP için yeterli).
func (l *Limiter) Wait(ctx context.Context) error {
	if l.interval == 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}

	l.mu.Lock()
	now := time.Now()
	nextAllowed := l.last.Add(l.interval)
	var wait time.Duration
	if now.Before(nextAllowed) {
		wait = nextAllowed.Sub(now)
	}
	l.last = now.Add(wait)
	l.mu.Unlock()

	if wait <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WithBackoff, verilen fn'i çalıştırır; hata dönerse exponential backoff + basit
// jitter ile maxRetries kez tekrar dener. crt.sh ve wayback gibi sık 503/429
// dönen kaynaklar için gereklidir.
func WithBackoff(ctx context.Context, maxRetries int, fn func() error) error {
	var err error
	base := 500 * time.Millisecond
	for attempt := 0; attempt <= maxRetries; attempt++ {
		err = fn()
		if err == nil {
			return nil
		}
		if attempt == maxRetries {
			break
		}
		backoff := base * time.Duration(1<<uint(attempt))
		// basit jitter: %20'ye kadar rastgele ekleme yerine sabit yarı-jitter
		jitter := backoff / 4
		wait := backoff + jitter
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
	return err
}

// AdaptiveLimiter, sabit hizli Limiter'in ustune "hedefin tepkisine gore
// kendini ayarlama" davranisi ekler:
//   - 429/403/baglanti-hatasi gorulunce hiz otomatik yariya iner (guvenli yavaslama)
//   - ardisik basarili isteklerde kademeli olarak (varsayilan hiza kadar) tekrar hizlanir
//   - ardisik blok esigine (varsayilan 3) ulasilinca ReportStatus true doner,
//     cagiran taraf bunu kullanicaya tek seferlik bir uyari olarak basabilir
//
// Amac IP/kimlik gizleyerek tespitten kacmak degil - hedefi asiri yuklemeyen,
// engellenince kendini toparlayan "iyi vatandas" davranisidir (bkz. proje notlari).
type AdaptiveLimiter struct {
	mu sync.Mutex

	baseInterval    time.Duration // kullanicinin verdigi/varsayilan guvenli hiz
	currentInterval time.Duration // su anki efektif hiz (adaptif olarak degisir)
	minInterval     time.Duration // asla bunun altina inmez (asiri hizlanmayi onler)
	maxInterval     time.Duration // asla bunun ustune cikmaz (sonsuz yavaslamayi onler)

	last time.Time

	consecutiveBlocks int
	consecutiveOK     int
	warnedThisStreak  bool
}

const blockStreakThreshold = 3     // bu kadar ardisik blok sinyalinde uyari verilir
const recoveryStreakStep    = 8    // bu kadar ardisik basarida hiz bir kademe artirilir

// NewAdaptive, perSecond baz alinarak bir AdaptiveLimiter olusturur.
// perSecond <= 0 ise guvenli varsayilan (saniyede 3 istek) kullanilir - hicbir
// zaman "sinirsiz/agresif" varsayilan uygulanmaz, kullanici acikca istemedikce.
func NewAdaptive(perSecond int) *AdaptiveLimiter {
	if perSecond <= 0 {
		perSecond = 3
	}
	base := time.Second / time.Duration(perSecond)
	return &AdaptiveLimiter{
		baseInterval:    base,
		currentInterval: base,
		minInterval:     base,          // varsayilan hizdan daha agresif hizlanmaz
		maxInterval:     base * 16,     // en fazla 16 kat yavaslar
	}
}

// Wait, mevcut efektif hiza gore bekler (adaptif olarak degisebilen interval).
func (a *AdaptiveLimiter) Wait(ctx context.Context) error {
	a.mu.Lock()
	interval := a.currentInterval
	now := time.Now()
	nextAllowed := a.last.Add(interval)
	var wait time.Duration
	if now.Before(nextAllowed) {
		wait = nextAllowed.Sub(now)
	}
	a.last = now.Add(wait)
	a.mu.Unlock()

	if wait <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ReportStatus, bir istegin sonucunu bildirir ve gerekirse hizi otomatik
// ayarlar. blocked=true (429/403/connection-reset gibi) gorulunce hiz
// yariya iner; basarili istekler kademeli olarak hizi toparlar.
// Donen bool, "bu streak icin ilk kez esik asildi, kullaniciya uyari bas"
// anlamina gelir (ayni streak icinde tekrar tekrar uyarmaz).
func (a *AdaptiveLimiter) ReportStatus(blocked bool) (shouldWarn bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if blocked {
		a.consecutiveBlocks++
		a.consecutiveOK = 0

		// Hizi yariya indir (interval'i 2 katina cikar), maxInterval'i asma.
		newInterval := a.currentInterval * 2
		if newInterval > a.maxInterval {
			newInterval = a.maxInterval
		}
		a.currentInterval = newInterval

		if a.consecutiveBlocks >= blockStreakThreshold && !a.warnedThisStreak {
			a.warnedThisStreak = true
			return true
		}
		return false
	}

	// Basarili istek: blok sayacini sifirla, kademeli hizlanma icin sayac artir.
	a.consecutiveBlocks = 0
	a.warnedThisStreak = false
	a.consecutiveOK++

	if a.consecutiveOK >= recoveryStreakStep {
		a.consecutiveOK = 0
		newInterval := a.currentInterval / 2
		if newInterval < a.minInterval {
			newInterval = a.minInterval
		}
		a.currentInterval = newInterval
	}
	return false
}
