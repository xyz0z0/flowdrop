package store

import (
	"path/filepath"
	"strings"
)

// Category is the inbox filter derived from an item's type and blob metadata.
// The mapping lives only on the server so every client sees the same answer
// (see the TYPE freeze in docs/TASKS.md).
type Category string

const (
	CategoryAll      Category = "all"
	CategoryText     Category = "text"
	CategoryLink     Category = "link"
	CategoryImage    Category = "image"
	CategoryVideo    Category = "video"
	CategoryAudio    Category = "audio"
	CategoryDocument Category = "document"
	CategoryArchive  Category = "archive"
	CategoryOther    Category = "other"
)

// allCategories is the accepted wire vocabulary, in display order.
var allCategories = []Category{
	CategoryAll, CategoryText, CategoryLink, CategoryImage,
	CategoryVideo, CategoryAudio, CategoryDocument, CategoryArchive, CategoryOther,
}

// Categories returns the accepted category values in display order.
func Categories() []Category {
	return append([]Category(nil), allCategories...)
}

// ParseCategory maps a query value to a Category. It is deliberately exact:
// anything outside the frozen vocabulary must be rejected, not guessed.
func ParseCategory(raw string) (Category, bool) {
	for _, c := range allCategories {
		if string(c) == raw {
			return c, true
		}
	}
	return "", false
}

// extCategories maps a lower-case file extension to its category. Known
// extensions win over the MIME type because sniffing is unreliable for several
// formats (flac, 7z and old Office files sniff as octet-stream, mkv sniffs as
// video/webm, and every ZIP container - docx, apk, jar - sniffs as
// application/zip).
//
// Installer and disk-image formats are pinned to "other" so a ZIP MIME cannot
// turn them into archives.
var extCategories = map[string]Category{
	// images
	".jpg": CategoryImage, ".jpeg": CategoryImage, ".png": CategoryImage,
	".gif": CategoryImage, ".webp": CategoryImage, ".bmp": CategoryImage,
	".tif": CategoryImage, ".tiff": CategoryImage, ".heic": CategoryImage,
	".heif": CategoryImage, ".avif": CategoryImage, ".svg": CategoryImage,

	// video
	".mp4": CategoryVideo, ".m4v": CategoryVideo, ".mov": CategoryVideo,
	".mkv": CategoryVideo, ".webm": CategoryVideo, ".avi": CategoryVideo,
	".flv": CategoryVideo, ".wmv": CategoryVideo, ".ts": CategoryVideo,
	".m2ts": CategoryVideo, ".3gp": CategoryVideo, ".mpg": CategoryVideo,
	".mpeg": CategoryVideo,

	// audio
	".mp3": CategoryAudio, ".m4a": CategoryAudio, ".aac": CategoryAudio,
	".flac": CategoryAudio, ".wav": CategoryAudio, ".ogg": CategoryAudio,
	".opus": CategoryAudio, ".wma": CategoryAudio, ".amr": CategoryAudio,
	".ape": CategoryAudio, ".aiff": CategoryAudio,

	// documents
	".pdf": CategoryDocument, ".doc": CategoryDocument, ".docx": CategoryDocument,
	".xls": CategoryDocument, ".xlsx": CategoryDocument, ".ppt": CategoryDocument,
	".pptx": CategoryDocument, ".txt": CategoryDocument, ".md": CategoryDocument,
	".csv": CategoryDocument, ".rtf": CategoryDocument, ".odt": CategoryDocument,
	".ods": CategoryDocument, ".odp": CategoryDocument, ".epub": CategoryDocument,

	// archives
	".zip": CategoryArchive, ".rar": CategoryArchive, ".7z": CategoryArchive,
	".tar": CategoryArchive, ".gz": CategoryArchive, ".tgz": CategoryArchive,
	".bz2": CategoryArchive, ".xz": CategoryArchive, ".zst": CategoryArchive,

	// installers and disk images: never archives, whatever the MIME type says
	".apk": CategoryOther, ".jar": CategoryOther, ".iso": CategoryOther,
	".exe": CategoryOther,
}

// CategoryOf derives the category of an item. The original item type wins for
// text, link and image; file items use the extension first and the MIME type
// only as a fallback.
func CategoryOf(itemType string, blob *Blob) Category {
	switch itemType {
	case "text":
		return CategoryText
	case "link":
		return CategoryLink
	case "image":
		return CategoryImage
	}
	if blob == nil {
		return CategoryOther
	}
	if ext := strings.ToLower(filepath.Ext(blob.OriginalName)); ext != "" {
		if c, ok := extCategories[ext]; ok {
			return c
		}
	}
	return categoryFromMIME(blob.ContentType)
}

// categoryFromMIME classifies by MIME type only, ignoring case and parameters.
func categoryFromMIME(contentType string) Category {
	mt := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	switch {
	case strings.HasPrefix(mt, "image/"):
		return CategoryImage
	case strings.HasPrefix(mt, "video/"):
		return CategoryVideo
	case strings.HasPrefix(mt, "audio/"):
		return CategoryAudio
	case strings.HasPrefix(mt, "text/"):
		return CategoryDocument
	case strings.HasPrefix(mt, "application/vnd.openxmlformats-officedocument."):
		return CategoryDocument
	case strings.HasPrefix(mt, "application/vnd.oasis.opendocument."):
		return CategoryDocument
	}
	switch mt {
	case "application/pdf", "application/msword", "application/rtf",
		"application/vnd.ms-excel", "application/vnd.ms-powerpoint",
		"application/epub+zip":
		return CategoryDocument
	case "application/zip", "application/x-zip-compressed", "application/x-rar-compressed",
		"application/vnd.rar", "application/x-7z-compressed", "application/gzip",
		"application/x-gzip", "application/x-tar", "application/x-bzip2",
		"application/x-xz", "application/zstd", "application/x-zstd":
		return CategoryArchive
	}
	return CategoryOther
}
