package filester

func NewFilesterScraper() *Scraper {
	s, _ := NewFilesterScraperWithOptions("", nil)
	return s
}
