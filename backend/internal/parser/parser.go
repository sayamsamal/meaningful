package parser

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"

	"meaningful-backend/internal/database"
)

// validWordPattern enforces that a word:
//   - Starts with an ASCII letter (digit-starting words are handled via allowlist)
//   - Contains only ASCII letters, digits, spaces, hyphens, apostrophes,
//     and common Latin diacritics (é, ñ, ü, ß, etc.)
//   - Is between 1 and 45 characters long
var validWordPattern = regexp.MustCompile(
	`^[A-Za-z]` + // must start with a letter (number-words use the allowlist)
		`[A-Za-z0-9 '\-` + // ASCII core characters
		`\x{00C0}-\x{00FF}` + // Latin-1 Supplement (À-ÿ): é, ñ, ü, ß, etc.
		`\x{0100}-\x{017F}` + // Latin Extended-A (Ā-ſ): ō, œ, š, ž, etc.
		`]*$`) // zero or more of the above

// allowlistedWords is loaded from data/allowlist.txt at init time.
// It contains words that would otherwise be rejected by the pattern filter
// (e.g. number-starting words like "101", or symbol-containing words like "Q&A").
// To add new exceptions, simply edit data/allowlist.txt — one word per line.
var allowlistedWords map[string]bool
var wordFrequencies map[string]float64

func init() {
	allowlistedWords = loadAllowlist("data/allowlist.txt")
	wordFrequencies = loadWordFrequencies("data/wordfreq.json")
}

func loadWordFrequencies(path string) map[string]float64 {
	result := make(map[string]float64)
	file, err := os.Open(path)
	if err != nil {
		log.Printf("Warning: could not load word frequencies from %s: %v", path, err)
		return result
	}
	defer file.Close()

	if err := json.NewDecoder(file).Decode(&result); err != nil {
		log.Printf("Warning: failed to parse word frequencies JSON: %v", err)
	} else {
		log.Printf("Loaded %d word frequencies", len(result))
	}
	return result
}

// loadAllowlist reads a text file of one-word-per-line entries into a lookup map.
// Blank lines and lines starting with # are ignored.
func loadAllowlist(path string) map[string]bool {
	result := make(map[string]bool)

	file, err := os.Open(path)
	if err != nil {
		log.Printf("Warning: could not load allowlist from %s: %v (no allowlist will be applied)", path, err)
		return result
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		result[line] = true
	}

	if err := scanner.Err(); err != nil {
		log.Printf("Warning: error reading allowlist: %v", err)
	}

	log.Printf("Loaded %d allowlisted words from %s", len(result), path)
	return result
}

// passesDictionaryWordFilter returns true if the word is suitable for
// an Oxford/Apple-style dictionary. It checks character validity, length,
// and consults the allowlist for common words with special symbols.
func passesDictionaryWordFilter(word string) bool {
	// Check the allowlist first for symbol-containing or number-starting exceptions
	if allowlistedWords[word] {
		return true
	}

	// Enforce length: 1–45 characters
	if len(word) == 0 || len([]rune(word)) > 45 {
		return false
	}

	return validWordPattern.MatchString(word)
}

type WiktextractWord struct {
	Word               string      `json:"word"`
	Pos                string      `json:"pos"`
	LangCode           string      `json:"lang_code"`
	EtymologyText      string              `json:"etymology_text"`
	EtymologyTemplates []EtymologyTemplate `json:"etymology_templates"`
	Senses             []Sense             `json:"senses"`
	Synonyms           []Linkage           `json:"synonyms"`
	Antonyms           []Linkage           `json:"antonyms"`
}

type EtymologyTemplate struct {
	Name      string `json:"name"`
	Expansion string `json:"expansion"`
}

type Sense struct {
	Glosses  []string  `json:"glosses"`
	Examples []Example `json:"examples"`
	Synonyms []Linkage `json:"synonyms"`
	Antonyms []Linkage `json:"antonyms"`
}

type Example struct {
	Text string `json:"text"`
}

type Linkage struct {
	Word string `json:"word"`
}

func StreamAndProcess(url string, batchSize int, processBatch func([]database.WordEntry) error) error {
	fmt.Printf("Downloading data from %s...\n", url)
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("failed to download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	gzReader, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	scanner := bufio.NewScanner(gzReader)
	const maxCapacity = 512 * 1024 * 1024 // 512MB max per line
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, maxCapacity)

	batch := make([]database.WordEntry, 0, batchSize)
	lineCount := 0
	skippedCount := 0

	var currentEntry *database.WordEntry

	for scanner.Scan() {
		line := scanner.Bytes()
		
		var raw WiktextractWord
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}

		if raw.LangCode != "en" {
			continue
		}

		// Apply the dictionary word filter
		if !passesDictionaryWordFilter(raw.Word) {
			skippedCount++
			continue
		}

		if currentEntry != nil && currentEntry.Word != raw.Word {
			finalizeEntry(currentEntry)
			batch = append(batch, *currentEntry)

			if len(batch) >= batchSize {
				if err := processBatch(batch); err != nil {
					return fmt.Errorf("failed to process batch: %w", err)
				}
				batch = batch[:0] 
				lineCount += batchSize
				fmt.Printf("Processed %d english entries...\n", lineCount)
			}
			currentEntry = nil
		}

		if currentEntry == nil {
			currentEntry = &database.WordEntry{
				Word:        raw.Word,
				Frequency:   wordFrequencies[raw.Word],
				Etymologies: []string{},
				Senses:      make(database.Senses),
				Synonyms:    []string{},
				Antonyms:    []string{},
			}
		}

		mergeEntry(currentEntry, raw)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner error: %w", err)
	}

	if currentEntry != nil {
		finalizeEntry(currentEntry)
		batch = append(batch, *currentEntry)
	}

	if len(batch) > 0 {
		if err := processBatch(batch); err != nil {
			return fmt.Errorf("failed to process final batch: %w", err)
		}
		lineCount += len(batch)
	}

	fmt.Printf("Finished. Total processed: %d | Skipped by filter: %d\n", lineCount, skippedCount)

	return nil
}

func mergeEntry(entry *database.WordEntry, raw WiktextractWord) {
	if raw.EtymologyText != "" {
		cleaned := cleanEtymology(raw.EtymologyText, raw.EtymologyTemplates)
		if cleaned != "" {
			entry.Etymologies = append(entry.Etymologies, cleaned)
		}
	}

	pos := raw.Pos
	if pos == "" {
		pos = "unknown"
	}

	if entry.Senses[pos] == nil {
		entry.Senses[pos] = []database.Definition{}
	}

	for _, sense := range raw.Senses {
		defStr := strings.Join(sense.Glosses, " ")
		if defStr == "" {
			continue
		}

		var exStrs []string
		for _, ex := range sense.Examples {
			if ex.Text != "" {
				exStrs = append(exStrs, ex.Text)
			}
		}

		if exStrs == nil {
			exStrs = []string{}
		}

		def := database.Definition{
			Definition: defStr,
			Examples:   exStrs,
		}

		entry.Senses[pos] = append(entry.Senses[pos], def)
		
		for _, syn := range sense.Synonyms {
			entry.Synonyms = append(entry.Synonyms, syn.Word)
		}
		for _, ant := range sense.Antonyms {
			entry.Antonyms = append(entry.Antonyms, ant.Word)
		}
	}

	for _, syn := range raw.Synonyms {
		entry.Synonyms = append(entry.Synonyms, syn.Word)
	}
	for _, ant := range raw.Antonyms {
		entry.Antonyms = append(entry.Antonyms, ant.Word)
	}
}

func finalizeEntry(entry *database.WordEntry) {
	entry.Etymologies = deduplicate(entry.Etymologies)
	entry.Synonyms = deduplicate(entry.Synonyms)
	entry.Antonyms = deduplicate(entry.Antonyms)
}

func deduplicate(slice []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, val := range slice {
		val = strings.TrimSpace(val)
		if val != "" && !seen[val] {
			seen[val] = true
			result = append(result, val)
		}
	}
	if result == nil {
		result = []string{}
	}
	return result
}

// cleanEtymology strips the "Etymology tree" diagram that Wiktextract prepends
// to etymology text. It leverages the parsed Wiktionary templates that actually
// generated the tree to find the exact string to remove.
// It then truncates extra sections like "Cognates" or "further possible etymology".
func cleanEtymology(text string, templates []EtymologyTemplate) string {
	cleaned := text

	// 1. Strip the exact Etymology tree expansion
	if strings.HasPrefix(text, "Etymology tree\n") {
		stripped := false
		for _, t := range templates {
			if strings.HasPrefix(t.Expansion, "Etymology tree\n") && strings.HasPrefix(text, t.Expansion) {
				cleaned = strings.TrimSpace(strings.TrimPrefix(text, t.Expansion))
				stripped = true
				break
			}
		}

		if !stripped {
			cleaned = strings.TrimSpace(strings.TrimPrefix(text, "Etymology tree\n"))
		}
	}

	// 2. Strip extra sections (like "Cognates") which are flattened from wikitext headings
	// to simple newline-separated text by Wiktextract. We truncate at the first occurrence.
	extraHeaders := []string{
		"\nCognates\n",
		"\ncognates\n",
		"\nFurther etymology",
		"\nfurther etymology",
		"\nFurther possible",
		"\nfurther possible",
		"\nDescendants\n",
		"\ndescendants\n",
		"\nAlternative forms\n",
		"\nalternative forms\n",
	}

	for _, header := range extraHeaders {
		if idx := strings.Index(cleaned, header); idx != -1 {
			cleaned = cleaned[:idx]
		}
	}

	return strings.TrimSpace(cleaned)
}
