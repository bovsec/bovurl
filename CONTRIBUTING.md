# Katkıda Bulunma

Katkılarınız için teşekkürler! bovurl'e katkıda bulunmak için:

## Yeni bir pasif kaynak eklemek

1. `internal/provider/` altına yeni bir dosya oluşturun (örn. `shodan.go`).
2. `Provider` interface'ini implement edin:
   ```go
   type Provider interface {
       Name() string
       Fetch(ctx context.Context, domain string) (<-chan types.URLResult, <-chan error)
   }
   ```
3. `provider.go` içindeki `doRequest`, `readAll`, `newHTTPClient` yardımcılarını
   kullanın (tutarlılık için).
4. `cmd/bovurl/main.go` içindeki `allProviderFactories` haritasına yeni
   kaynağınızı ekleyin.
5. Mümkünse `httptest.NewServer` ile mock'lanmış bir unit test ekleyin
   (gerçek API'ye her testte istek atmayın).

## Hata bildirimi

Issue açarken lütfen şunları belirtin:
- Kullandığınız komut ve flag'ler
- Beklenen davranış vs gerçekleşen davranış
- Go versiyonu (`go version`)

## Kod stili

- `gofmt -l -w .` çalıştırmadan PR açmayın.
- Yeni kod için harici bağımlılık eklemekten kaçının — proje bilinçli olarak
  sadece Go standart kütüphanesini kullanıyor.
- Değişikliklerinizle ilgili `go test ./...` çalıştırıp geçtiğinden emin olun.

## Pull Request süreci

1. Fork edin, feature branch açın (`git checkout -b feature/yeni-kaynak`).
2. Değişikliklerinizi yapın, test ekleyin.
3. PR açın, ne değiştiğini ve neden kısaca açıklayın.
