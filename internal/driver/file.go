package driver

import (
	"context"
	"io"
	"log/slog"
)

// FileRoot describes a storage root exposed by a printer.
type FileRoot struct {
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Writable      bool           `json:"writable"`
	CapacityBytes *int64         `json:"capacityBytes"`
	FreeBytes     *int64         `json:"freeBytes"`
	Metadata      map[string]any `json:"metadata"`
}

// FileEntryType classifies a device file entry.
type FileEntryType string

const (
	FileEntryTypeFile      FileEntryType = "file"
	FileEntryTypeDirectory FileEntryType = "directory"
	FileEntryTypeUnknown   FileEntryType = "unknown"
)

// FileEntry describes a single file or directory on printer storage.
type FileEntry struct {
	Name       string         `json:"name"`
	Root       string         `json:"root"`
	Path       string         `json:"path"`
	DevicePath string         `json:"devicePath"`
	Type       FileEntryType  `json:"type"`
	SizeBytes  *int64         `json:"sizeBytes"`
	ModifiedAt *string        `json:"modifiedAt"`
	Metadata   map[string]any `json:"metadata"`
}

// FileListResult is returned by FileDriver.FileList.
type FileListResult struct {
	Entries []FileEntry `json:"entries"`
}

// FileTransferResult is returned by FileDriver.FileDownload and FileUpload.
type FileTransferResult struct {
	BytesTransferred *int64 `json:"bytesTransferred"`
}

// FileDriver extends Driver with file management operations.
// Drivers that support file capabilities implement this interface.
type FileDriver interface {
	Driver

	// FileRoots returns the storage roots available on the printer.
	FileRoots(
		ctx context.Context,
		p ProfileInput,
		s SecretsBundle,
		log *slog.Logger,
	) ([]FileRoot, error)

	// FileList lists files at the given root and path.
	// path is the normalized path within the root (e.g. "/" or "/models").
	FileList(
		ctx context.Context,
		p ProfileInput,
		s SecretsBundle,
		root string,
		path string,
		recursive bool,
		log *slog.Logger,
	) (*FileListResult, error)

	// FileDownload downloads a file from the printer to the provided writer.
	// path is the normalized path within the root.
	FileDownload(
		ctx context.Context,
		p ProfileInput,
		s SecretsBundle,
		root string,
		path string,
		dst io.Writer,
		log *slog.Logger,
	) (*FileTransferResult, error)

	// FileUpload uploads a file to the printer from the provided reader.
	// path is the normalized path within the root.
	// size is the number of bytes to upload (-1 if unknown).
	FileUpload(
		ctx context.Context,
		p ProfileInput,
		s SecretsBundle,
		root string,
		path string,
		src io.Reader,
		size int64,
		overwrite bool,
		log *slog.Logger,
	) (*FileTransferResult, error)
}
