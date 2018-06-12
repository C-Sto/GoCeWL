package libgocewl

type Config struct {
	Threads   int
	Url       string
	Localpath string
	ProxyAddr string
	SSLIgnore bool
	Depth     int
	Host      string
}
