package scrape

import "math/rand"

// HeaderProfile represents a complete set of HTTP headers for a device/browser combination
type HeaderProfile struct {
	UserAgent       string
	Accept          string
	AcceptLanguage  string
	AcceptEncoding  string
	SecFetchDest    string
	SecFetchMode    string
	SecFetchSite    string
	SecFetchUser    string
	SecChUa         string
	SecChUaMobile   string
	SecChUaPlatform string
}

// HeaderStrategy represents different approach styles for scraping
type HeaderStrategy string

const (
	StrategyModernBrowser HeaderStrategy = "modern_browser"
	StrategyMobileDevice  HeaderStrategy = "mobile_device"
	StrategyBotFriendly   HeaderStrategy = "bot_friendly"
)

var modernBrowserProfiles = []HeaderProfile{
	{
		UserAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		Accept:          "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
		AcceptLanguage:  "en-US,en;q=0.9",
		AcceptEncoding:  "gzip, deflate, br, zstd",
		SecFetchDest:    "document",
		SecFetchMode:    "navigate",
		SecFetchSite:    "none",
		SecFetchUser:    "?1",
		SecChUa:         `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"macOS"`,
	},
	{
		UserAgent:       "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
		Accept:          "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
		AcceptLanguage:  "en-US,en;q=0.9",
		AcceptEncoding:  "gzip, deflate, br, zstd",
		SecFetchDest:    "document",
		SecFetchMode:    "navigate",
		SecFetchSite:    "none",
		SecFetchUser:    "?1",
		SecChUa:         `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		SecChUaMobile:   "?0",
		SecChUaPlatform: `"Windows"`,
	},
	{
		UserAgent:       "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.2 Safari/605.1.15",
		Accept:          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		AcceptLanguage:  "en-US,en;q=0.9",
		AcceptEncoding:  "gzip, deflate, br",
		SecFetchDest:    "document",
		SecFetchMode:    "navigate",
		SecFetchSite:    "none",
		SecFetchUser:    "",
		SecChUa:         "",
		SecChUaMobile:   "",
		SecChUaPlatform: "",
	},
}

var mobileDeviceProfiles = []HeaderProfile{
	{
		UserAgent:       "Mozilla/5.0 (iPhone; CPU iPhone OS 18_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.2 Mobile/15E148 Safari/604.1",
		Accept:          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		AcceptLanguage:  "en-US,en;q=0.9",
		AcceptEncoding:  "gzip, deflate, br",
		SecFetchDest:    "document",
		SecFetchMode:    "navigate",
		SecFetchSite:    "none",
		SecFetchUser:    "",
		SecChUa:         "",
		SecChUaMobile:   "?1",
		SecChUaPlatform: `"iOS"`,
	},
	{
		UserAgent:       "Mozilla/5.0 (iPhone; CPU iPhone OS 17_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.7 Mobile/15E148 Safari/604.1",
		Accept:          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		AcceptLanguage:  "en-US,en;q=0.9",
		AcceptEncoding:  "gzip, deflate, br",
		SecFetchDest:    "document",
		SecFetchMode:    "navigate",
		SecFetchSite:    "none",
		SecFetchUser:    "",
		SecChUa:         "",
		SecChUaMobile:   "?1",
		SecChUaPlatform: `"iOS"`,
	},
	{
		UserAgent:       "Mozilla/5.0 (iPad; CPU OS 18_2 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.2 Mobile/15E148 Safari/604.1",
		Accept:          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		AcceptLanguage:  "en-US,en;q=0.9",
		AcceptEncoding:  "gzip, deflate, br",
		SecFetchDest:    "document",
		SecFetchMode:    "navigate",
		SecFetchSite:    "none",
		SecFetchUser:    "",
		SecChUa:         "",
		SecChUaMobile:   "?1",
		SecChUaPlatform: `"iOS"`,
	},
	{
		UserAgent:       "Mozilla/5.0 (Linux; Android 14; Pixel 8 Pro) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36",
		Accept:          "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
		AcceptLanguage:  "en-US,en;q=0.9",
		AcceptEncoding:  "gzip, deflate, br, zstd",
		SecFetchDest:    "document",
		SecFetchMode:    "navigate",
		SecFetchSite:    "none",
		SecFetchUser:    "?1",
		SecChUa:         `"Google Chrome";v="131", "Chromium";v="131", "Not_A Brand";v="24"`,
		SecChUaMobile:   "?1",
		SecChUaPlatform: `"Android"`,
	},
}

var botFriendlyProfiles = []HeaderProfile{
	{
		UserAgent:       "SupacrawlerBot/1.0 (+https://supacrawler.com/bot)",
		Accept:          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		AcceptLanguage:  "en-US,en;q=0.9",
		AcceptEncoding:  "gzip, deflate, br",
		SecFetchDest:    "",
		SecFetchMode:    "",
		SecFetchSite:    "",
		SecFetchUser:    "",
		SecChUa:         "",
		SecChUaMobile:   "",
		SecChUaPlatform: "",
	},
	{
		UserAgent:       "Mozilla/5.0 (compatible; SupacrawlerBot/1.0; +https://supacrawler.com/bot)",
		Accept:          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
		AcceptLanguage:  "en-US,en;q=0.9",
		AcceptEncoding:  "gzip, deflate, br",
		SecFetchDest:    "",
		SecFetchMode:    "",
		SecFetchSite:    "",
		SecFetchUser:    "",
		SecChUa:         "",
		SecChUaMobile:   "",
		SecChUaPlatform: "",
	},
}

// GetHeaderProfile returns a random header profile for the given strategy
func GetHeaderProfile(strategy HeaderStrategy) HeaderProfile {
	switch strategy {
	case StrategyModernBrowser:
		return modernBrowserProfiles[rand.Intn(len(modernBrowserProfiles))]
	case StrategyMobileDevice:
		return mobileDeviceProfiles[rand.Intn(len(mobileDeviceProfiles))]
	case StrategyBotFriendly:
		return botFriendlyProfiles[rand.Intn(len(botFriendlyProfiles))]
	default:
		return modernBrowserProfiles[0]
	}
}

// GetAllStrategies returns all available strategies in order of preference
func GetAllStrategies() []HeaderStrategy {
	return []HeaderStrategy{
		StrategyModernBrowser,
		StrategyMobileDevice,
		StrategyBotFriendly,
	}
}
