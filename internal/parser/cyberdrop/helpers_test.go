package cyberdrop

func NewCyberDropScraper() *Scraper {
	s, _ := NewCyberDropScraperWithOptions("", nil)
	return s
}
