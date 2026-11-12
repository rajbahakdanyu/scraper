package downloader

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

// downloadOnce performs a single download attempt to tempPath, fetching
// resolvedURL (the item's final download URL, produced by its Resolve func
// when it has one). The request and the response body read are both bound
// to ctx, so cancelling ctx aborts an in-flight attempt immediately
// instead of waiting for it to finish. If tempPath already has bytes on
// disk (a previous attempt left them there), it requests the remainder via
// a Range header and appends; if the server doesn't honor the range (a
// plain 200 response) it falls back to a full re-download instead of
// corrupting the file.
func (d *Downloader) downloadOnce(ctx context.Context, item model.MediaItem, resolvedURL, tempPath string) (string, error) {
	var existing int64
	var resume resumeMetadata
	if info, err := os.Stat(tempPath); err == nil {
		existing = info.Size()
	}
	if existing > 0 {
		metadata, err := readResumeMetadata(tempPath)
		// Items with a Resolve func get a freshly signed URL every run, so
		// the recorded URL legitimately differs from this run's resolved
		// URL. What actually protects resume integrity is the validator
		// (ETag/Last-Modified, sent as If-Range and re-checked against the
		// response) — URL equality is only an extra guard for items whose
		// URL is final and stable.
		urlMismatch := item.Resolve == nil && metadata.URL != resolvedURL
		identityMismatch := metadata.ItemIdentity != "" && metadata.ItemIdentity != itemIdentity(item)
		unverifiedResolvedPartial := item.Resolve != nil && metadata.ItemIdentity != itemIdentity(item)
		if err != nil || metadata.validator() == "" || urlMismatch || identityMismatch || unverifiedResolvedPartial {
			existing = 0
		} else {
			resume = metadata
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", resolvedURL, nil)
	if err != nil {
		return "", err
	}
	if d.userAgent != "" {
		req.Header.Set("User-Agent", d.userAgent)
	}
	if item.Referer != "" {
		req.Header.Set("Referer", item.Referer)
	}
	if existing > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", existing))
		req.Header.Set("If-Range", resume.validator())
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return "", redactConnectionError(err, resolvedURL)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// The server ignored our Range request (or we didn't send one).
		// Bytes already on disk can't be trusted to precede this
		// full-content response, so start over from scratch.
		existing = 0
	case http.StatusPartialContent:
		start, _, _, err := parseContentRange(resp.Header.Get("Content-Range"))
		if existing == 0 || err != nil || start != existing || !resume.matches(resp) {
			cleanupErr := removeFiles(tempPath, resumeMetadataPath(tempPath))
			return "", errors.Join(fmt.Errorf("invalid partial response for %s; discarded stale partial", redactURL(resolvedURL)), cleanupErr)
		}
	case http.StatusRequestedRangeNotSatisfiable:
		// A 416 whose Content-Range total equals our on-disk byte count
		// means the partial already IS the complete resource (a previous
		// run finished the body but died before the rename). Keep it and
		// let Download rename it into place instead of re-downloading
		// everything. The validator must still match, so a resource that
		// changed to the same size doesn't get trusted by accident.
		if existing > 0 && resume.matches(resp) {
			if total, err := parseContentRangeTotal(resp.Header.Get("Content-Range")); err == nil && total == existing {
				return hashFileDigest(tempPath)
			}
		}
		// Our recorded partial no longer lines up with the remote resource
		// (e.g. it changed size) — discard it so the next retry attempt
		// fetches the whole thing fresh.
		cleanupErr := removeFiles(tempPath, resumeMetadataPath(tempPath))
		return "", errors.Join(fmt.Errorf("HTTP %d for %s (stale partial download discarded)", resp.StatusCode, redactURL(resolvedURL)), cleanupErr)
	default:
		err := fmt.Errorf("HTTP %d for %s", resp.StatusCode, redactURL(resolvedURL))
		if !model.IsRetryableStatus(resp.StatusCode) {
			return "", model.NewNonRetryableStatus(err, resp.StatusCode)
		}
		return "", model.NewHTTPStatusError(
			err,
			resp.StatusCode,
			redactURL(resolvedURL),
			parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		)
	}

	total := existing + resp.ContentLength
	if resp.ContentLength < 0 {
		total = existing
	}
	if _, _, fromHeader, err := parseContentRange(resp.Header.Get("Content-Range")); err == nil {
		total = fromHeader
	}
	// The response carried no length of its own (chunked/HTTP2): fall back
	// to the scraper-reported item size so a silently truncated body is
	// still caught by the size check below. Skip this when the transport
	// transparently decompressed the body — decompressed bytes legitimately
	// differ from the on-the-wire size the scraper reported.
	if total <= 0 && item.Size > 0 && !resp.Uncompressed {
		total = item.Size
	}

	flags := os.O_CREATE | os.O_WRONLY
	if existing > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	out, err := fileutil.OpenFile(tempPath, flags, 0644)
	if err != nil {
		// A local filesystem failure won't be fixed by re-fetching.
		return "", model.NewNonRetryable(err)
	}
	if existing == 0 {
		resume = metadataFromResponse(resolvedURL, resp, item.Resolve == nil)
		resume.ItemIdentity = itemIdentity(item)
		if resume.validator() == "" {
			if err := removeFiles(resumeMetadataPath(tempPath)); err != nil {
				return "", errors.Join(err, out.Close())
			}
		} else if err := writeResumeMetadata(tempPath, resume); err != nil {
			return "", errors.Join(err, out.Close())
		}
	}

	pr := &progressReader{
		reader: resp.Body,
		base:   existing,
		onProgress: func(bytes int64) {
			d.sendProgress(model.ProgressEvent{
				Item:   item,
				Bytes:  bytes,
				Total:  total,
				Status: model.StatusDownloading,
			})
		},
		limiter: d.Core.Limiter(),
		ctx:     ctx,
	}

	hasher := sha256.New()
	if existing > 0 {
		if err := seedHasher(tempPath, hasher); err != nil {
			return "", errors.Join(err, out.Close())
		}
	}
	tee := io.TeeReader(pr, hasher)
	var source io.Reader = tee
	var unknownSizeLimit *io.LimitedReader
	unknownSize := total <= 0 || (existing > 0 && resp.ContentLength < 0)
	if unknownSize {
		unknownSizeLimit = &io.LimitedReader{R: tee, N: MaxUnknownFileSize + 1}
		source = unknownSizeLimit
	}

	// Sniff the first bytes: a payload that claims to be media but is
	// actually HTML or an executable is refused before it is kept. A
	// single Read (not ReadFull) is deliberate: it must never block
	// waiting for a full prefix on a stream that stays open.
	head := make([]byte, contentSniffLen)
	headBytes, headErr := source.Read(head)
	if headBytes == 0 && headErr != nil && headErr != io.EOF {
		return "", errors.Join(headErr, out.Close())
	}
	head = head[:headBytes]
	// Only fresh starts are judged: a resumed Range response begins
	// mid-file, where any bytes are legitimate.
	if existing == 0 {
		if denied := deniedContentType(head); denied != "" && model.IsAllowedMediaItem(item) {
			err := model.NewNonRetryable(fmt.Errorf("content is %s, not media: %s", denied, redactURL(resolvedURL)))
			return "", errors.Join(err, out.Close())
		}
	}
	if headBytes > 0 {
		if _, err := out.Write(head); err != nil {
			return "", errors.Join(err, out.Close())
		}
	}
	// The bytes are kept so a later attempt can resume after them.
	if headErr != nil && headErr != io.EOF {
		return "", errors.Join(headErr, out.Close())
	}

	copiedBytes, copyErr := io.Copy(out, source)
	copiedBytes += int64(headBytes)
	closeErr := out.Close()
	if unknownSizeLimit != nil && copiedBytes > MaxUnknownFileSize {
		err := model.NewNonRetryable(fmt.Errorf("download exceeds maximum unknown file size of %d bytes", MaxUnknownFileSize))
		return "", errors.Join(err, closeErr)
	}
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if total > 0 {
		info, statErr := os.Stat(tempPath)
		if statErr != nil {
			return "", statErr
		}
		if info.Size() != total {
			return "", fmt.Errorf("download size mismatch for %s: got %d, want %d", redactURL(resolvedURL), info.Size(), total)
		}
	}

	// A fresh download is re-checked on the completed file: the
	// pre-transfer sniff only sees the first Read, so a payload padded
	// past it would otherwise slip through. Resumed downloads are
	// exempt — their first bytes are mid-file (trusted, recorded
	// decision).
	if existing == 0 {
		// A fresh download that carries no bytes at all is a CDN glitch (a
		// 200 with an empty body), not a file: recording it as a success
		// renames an empty .part into place and silently loses the download.
		// Fail retryably instead, so the retry machinery either gets bytes
		// or fails loudly into the retry list. Resumed downloads are exempt
		// — they already have bytes on disk.
		if existing == 0 && copiedBytes == 0 && model.IsAllowedMediaItem(item) {
			return "", fmt.Errorf("empty response body (0 bytes) from %s", redactURL(resolvedURL))
		}

		if denyErr := refuseSuspiciousContent(tempPath, item, resolvedURL); denyErr != nil {
			_ = os.Remove(tempPath) // a refused payload must never resume
			return "", denyErr
		}
	}

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

func seedHasher(path string, hasher io.Writer) error {
	file, err := fileutil.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(hasher, file)
	return err
}

func hashFileDigest(path string) (string, error) {
	hasher := sha256.New()
	if err := seedHasher(path, hasher); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}
