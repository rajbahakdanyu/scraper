package turbo

func NewTurboScraper() *Scraper {
	s, _ := NewTurboScraperWithOptions("", nil)
	return s
}
