package metrics

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"megaapp-back/internal/platform/appendfile"
)

const outboxChunkLimitBytes int64 = 950 * 1000

type outbox struct {
	basePath   string
	activePath string
	activeSize int64
	nextIndex  int
	limitBytes int64
}

type outboxLine struct {
	Service      string             `json:"service"`
	MinuteBucket int64              `json:"minuteBucket"`
	Metrics      map[string]float64 `json:"metrics"`
}

type numberedOutboxFile struct {
	index int
	path  string
	size  int64
}

func prepareOutbox(basePath string, limitBytes int64) (*outbox, error) {
	outboxFiles, err := listNumberedOutboxFiles(basePath)
	if err != nil {
		return nil, err
	}

	box := &outbox{basePath: basePath, limitBytes: limitBytes}

	if len(outboxFiles) == 0 {
		box.activePath = numberedOutboxPath(basePath, 1)
		box.nextIndex = 2
		return box, nil
	}

	lastFile := outboxFiles[len(outboxFiles)-1]
	box.nextIndex = lastFile.index + 1
	if lastFile.size >= limitBytes {
		box.activePath = numberedOutboxPath(basePath, box.nextIndex)
		box.nextIndex++
		return box, nil
	}

	box.activePath = lastFile.path
	box.activeSize = lastFile.size
	return box, nil
}

func appendOutboxLine(box *outbox, service string, snapshot MinuteSnapshot) error {
	encoded, err := encodeOutboxLine(service, snapshot)
	if err != nil {
		return err
	}

	if box.activeSize > 0 && box.activeSize+int64(len(encoded)) > box.limitBytes {
		box.activePath = numberedOutboxPath(box.basePath, box.nextIndex)
		box.activeSize = 0
		box.nextIndex++
	}

	if err := appendfile.Append(box.activePath, encoded); err != nil {
		return fmt.Errorf("append metrics snapshot: %w", err)
	}

	box.activeSize += int64(len(encoded))
	if box.activeSize >= box.limitBytes {
		box.activePath = numberedOutboxPath(box.basePath, box.nextIndex)
		box.activeSize = 0
		box.nextIndex++
	}

	return nil
}

func encodeOutboxLine(service string, snapshot MinuteSnapshot) ([]byte, error) {
	encoded, err := json.Marshal(outboxLine{Service: service, MinuteBucket: snapshot.MinuteBucket, Metrics: snapshot.Metrics})
	if err != nil {
		return nil, fmt.Errorf("encode metrics snapshot: %w", err)
	}
	return append(encoded, '\n'), nil
}

func readPendingSnapshots(basePath, service string, lastAcked int64) ([]MinuteSnapshot, error) {
	files, err := listOutboxFiles(basePath)
	if err != nil {
		return nil, err
	}

	byBucket := make(map[int64]MinuteSnapshot)
	for _, path := range files {
		if err := readOutboxFile(path, func(line outboxLine) error {
			if strings.TrimSpace(line.Service) == "" {
				return errors.New("missing service")
			}
			if line.Service != service {
				return nil
			}
			if line.MinuteBucket <= 0 || len(line.Metrics) == 0 {
				return errors.New("missing minuteBucket or metrics")
			}
			for name, value := range line.Metrics {
				if strings.TrimSpace(name) == "" || math.IsNaN(value) || math.IsInf(value, 0) {
					return errors.New("invalid metric")
				}
			}
			if line.MinuteBucket <= lastAcked {
				return nil
			}
			byBucket[line.MinuteBucket] = MinuteSnapshot{
				MinuteBucket: line.MinuteBucket,
				Metrics:      line.Metrics,
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}

	pending := make([]MinuteSnapshot, 0, len(byBucket))
	for _, snapshot := range byBucket {
		pending = append(pending, snapshot)
	}
	slices.SortFunc(pending, func(left, right MinuteSnapshot) int {
		return cmp.Compare(left.MinuteBucket, right.MinuteBucket)
	})
	return pending, nil
}

func listOutboxFiles(basePath string) ([]string, error) {
	files := make([]string, 0)
	if _, err := os.Stat(basePath); err == nil {
		files = append(files, basePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat legacy metrics outbox: %w", err)
	}

	numbered, err := listNumberedOutboxFiles(basePath)
	if err != nil {
		return nil, err
	}
	for _, file := range numbered {
		files = append(files, file.path)
	}
	return files, nil
}

func readOutboxFile(path string, use func(outboxLine) error) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open metrics outbox %q: %w", path, err)
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for lineNumber := 1; ; lineNumber++ {
		encoded, readErr := reader.ReadBytes('\n')
		if len(encoded) > 0 {
			if strings.TrimSpace(string(encoded)) == "" {
				if readErr != nil {
					break
				}
				continue
			}
			var line outboxLine
			if err := json.Unmarshal(encoded, &line); err != nil {
				return fmt.Errorf("decode metrics outbox %q line %d: %w", path, lineNumber, err)
			}
			if err := use(line); err != nil {
				return fmt.Errorf("decode metrics outbox %q line %d: %w", path, lineNumber, err)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return fmt.Errorf("read metrics outbox %q: %w", path, readErr)
		}
	}
	return nil
}

func listNumberedOutboxFiles(basePath string) ([]numberedOutboxFile, error) {
	dir := filepath.Dir(basePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read metrics outbox dir: %w", err)
	}

	files := make([]numberedOutboxFile, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		index, ok := parseNumberedOutboxPath(basePath, entry.Name())
		if !ok {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat metrics outbox: %w", err)
		}

		files = append(files, numberedOutboxFile{
			index: index,
			path:  filepath.Join(dir, entry.Name()),
			size:  info.Size(),
		})
	}

	slices.SortFunc(files, func(left numberedOutboxFile, right numberedOutboxFile) int {
		return left.index - right.index
	})

	return files, nil
}

func numberedOutboxPath(basePath string, index int) string {
	ext := filepath.Ext(basePath)
	base := strings.TrimSuffix(basePath, ext)
	return fmt.Sprintf("%s.%02d%s", base, index, ext)
}

func parseNumberedOutboxPath(basePath string, fileName string) (int, bool) {
	ext := filepath.Ext(basePath)
	baseName := strings.TrimSuffix(filepath.Base(basePath), ext)
	prefix := baseName + "."
	if !strings.HasPrefix(fileName, prefix) || !strings.HasSuffix(fileName, ext) {
		return 0, false
	}

	rawIndex := strings.TrimSuffix(strings.TrimPrefix(fileName, prefix), ext)
	if rawIndex == "" {
		return 0, false
	}

	index, err := strconv.Atoi(rawIndex)
	if err != nil || index < 1 {
		return 0, false
	}

	return index, true
}
