package goonbox

func NewGoonBoxScraper() *Scraper {
	s, _ := NewGoonBoxScraperWithOptions("", nil)
	return s
}
