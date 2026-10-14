package pixeldrain

func NewPixeldrainScraper() *Scraper {
	s, _ := NewPixeldrainScraperWithOptions("", nil)
	return s
}
