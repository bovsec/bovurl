package banner

import "fmt"

const (
	colorReset  = "\033[0m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
	colorYellow = "\033[33m"
	colorGray   = "\033[90m"
)

const Version = "v0.1.0"

// asciiArt "BOVURL" yazısının blok harflerle çizimi.
const asciiArt = `
 ____   _____  __      __ _   _  _____   __
|  _ \ / _ \ \/ /  \  / /| | | ||  __ \ / /
| |_) | | | \  /    \/  / | | | || |  | | |
|  _ <| |_| /  \    /\  /  | |_| || |__| | |
|_.__/ \___/_/\_\  /_/\_\   \___/ |_____/|_|
`

// Print rengi kapatılmadıysa (NoColor=false) renkli, aksi halde düz metin basar.
func Print(noColor bool) {
	if noColor {
		fmt.Println(asciiArt)
		fmt.Printf("bovurl %s - pasif + aktif URL kesif araci\n", Version)
		fmt.Println("projectdiscovery tarzi, tek binary")
		fmt.Println("--------------------------------------------------")
		return
	}
	fmt.Println(colorCyan + colorBold + asciiArt + colorReset)
	fmt.Println(colorYellow + colorBold + "  bovurl " + Version + colorReset + colorGray + "  - pasif + aktif URL kesif araci" + colorReset)
	fmt.Println(colorGray + "  wayback . commoncrawl . alienvault . otx . urlscan . crt.sh . active-crawl" + colorReset)
	fmt.Println(colorGray + "  --------------------------------------------------------------" + colorReset)
}

// Warn kullaniciya sorumlu kullanim uyarisi gosterir (katana/urlfinder gelenegi).
func Warn(noColor bool) {
	msg := "[WRN] Sadece yetkiniz olan hedeflerde kullanin. Sorumluluk size aittir."
	if noColor {
		fmt.Println(msg)
		return
	}
	fmt.Println("\033[31m" + msg + colorReset)
}
