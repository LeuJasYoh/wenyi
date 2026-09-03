package cli

import (
	"wenyi/internal/ingest"
)

func loadTitleDoc(input, tmp string) (titleDoc, error) {
	doc, err := ingest.LoadDocument(input, "auto", "zh", 0, tmp)
	if err != nil {
		return titleDoc{}, err
	}
	return titleDoc{Title: doc.Title}, nil
}
