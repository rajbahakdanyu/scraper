package bunker

func NewBunkerScraper() *Scraper {
	s, _ := NewBunkerScraperWithOptions("", nil)
	return s
}
