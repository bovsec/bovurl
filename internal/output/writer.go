// Package output, pipeline'ın son adımıdır: sonuçları akarken (streaming)
// diske veya stdout'a yazar. Büyük taramalarda tüm sonuçları belleğe alıp
// sona yazmak yerine, her sonuç geldiğinde hemen flush edilir.
package output

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/you/bovurl/internal/types"
)

// Write, gelen kanaldaki her URLResult'u ya JSONL ya da düz metin formatında
// yazar. path boşsa stdout'a yazar. Döndürdüğü int, yazılan toplam sonuç
// sayısıdır (özet için kullanışlı).
func Write(in <-chan types.URLResult, path string, jsonl bool, silent bool) (int, error) {
	var w io.Writer = os.Stdout
	var f *os.File
	if path != "" {
		var err error
		f, err = os.Create(path)
		if err != nil {
			return 0, fmt.Errorf("cikti dosyasi olusturulamadi: %w", err)
		}
		defer f.Close()
		w = f
	}

	bufWriter := bufio.NewWriter(w)
	defer bufWriter.Flush()

	enc := json.NewEncoder(bufWriter)
	count := 0

	for r := range in {
		count++
		if jsonl {
			if err := enc.Encode(r); err != nil {
				return count, fmt.Errorf("jsonl yazma hatasi: %w", err)
			}
		} else {
			line := r.URL
			if r.IsForm {
				line = fmt.Sprintf("%s [FORM: %v]", r.URL, r.FormFields)
			}
			if _, err := fmt.Fprintln(bufWriter, line); err != nil {
				return count, fmt.Errorf("yazma hatasi: %w", err)
			}
		}
		// Terminale de aninda gorunmesi icin (dosyaya yazarken de kullanici
		// ilerlemeyi gormek isteyebilir) - path bossa zaten stdout'a yaziyoruz,
		// path doluysa ve silent degilse ayrica ekrana da basalim.
		if path != "" && !silent {
			fmt.Println(r.URL)
		}
		// Her N sonucta bir flush yaparak bellekte asiri birikmeyi onle.
		if count%500 == 0 {
			bufWriter.Flush()
		}
	}

	return count, nil
}
