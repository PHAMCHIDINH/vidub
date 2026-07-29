package storage

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// DriveClient handles Google Drive upload operations.
type DriveClient struct {
	srv      *drive.Service
	folderID string
}

// NewDriveClient creates a new DriveClient using a service account credentials file.
// On first use, it finds or creates the target folder.
func NewDriveClient(credentialsPath, folderName string) (*DriveClient, error) {
	if _, err := os.Stat(credentialsPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("Google Drive credentials not found at %s — set GOOGLE_DRIVE_CREDENTIALS or disable with GOOGLE_DRIVE_ENABLED=false", credentialsPath)
	}

	ctx := context.Background()
	b, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}

	config, err := google.JWTConfigFromJSON(b, drive.DriveFileScope)
	if err != nil {
		return nil, fmt.Errorf("parse credentials: %w", err)
	}

	client := config.Client(ctx)
	srv, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("create drive service: %w", err)
	}

	dc := &DriveClient{srv: srv}

	// Find or create folder.
	folderID, err := dc.findOrCreateFolder(folderName)
	if err != nil {
		return nil, fmt.Errorf("init folder %q: %w", folderName, err)
	}
	dc.folderID = folderID

	log.Printf("[gdrive] ready — folder %q (id: %s)", folderName, folderID)
	return dc, nil
}

// UploadFile uploads a file to Google Drive and returns a shareable view link.
// It creates a subfolder named after the jobID inside the root folder.
func (d *DriveClient) UploadFile(localPath, jobID, mimeType string) (string, error) {
	ctx := context.Background()

	// Create job subfolder.
	jobFolderID, err := d.findOrCreateSubfolder(d.folderID, jobID)
	if err != nil {
		return "", fmt.Errorf("create job folder: %w", err)
	}

	filename := filepath.Base(localPath)

	f, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	driveFile := &drive.File{
		Name:     filename,
		Parents:  []string{jobFolderID},
		MimeType: mimeType,
	}

	created, err := d.srv.Files.Create(driveFile).Media(f).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("upload: %w", err)
	}

	// Set shareable permission.
	perm := &drive.Permission{
		Type: "anyone",
		Role: "reader",
	}
	_, err = d.srv.Permissions.Create(created.Id, perm).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("set permission: %w", err)
	}

	link := fmt.Sprintf("https://drive.google.com/file/d/%s/view", created.Id)
	log.Printf("[gdrive] uploaded %s → %s", filename, link)
	return link, nil
}

func (d *DriveClient) findOrCreateFolder(name string) (string, error) {
	ctx := context.Background()

	q := fmt.Sprintf("name='%s' and mimeType='application/vnd.google-apps.folder' and trashed=false", name)
	r, err := d.srv.Files.List().Q(q).PageSize(1).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("search folder: %w", err)
	}

	if len(r.Files) > 0 {
		return r.Files[0].Id, nil
	}

	folder := &drive.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
	}
	created, err := d.srv.Files.Create(folder).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("create folder: %w", err)
	}

	return created.Id, nil
}

func (d *DriveClient) findOrCreateSubfolder(parentID, name string) (string, error) {
	ctx := context.Background()

	q := fmt.Sprintf("name='%s' and mimeType='application/vnd.google-apps.folder' and '%s' in parents and trashed=false", name, parentID)
	r, err := d.srv.Files.List().Q(q).PageSize(1).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("search subfolder: %w", err)
	}

	if len(r.Files) > 0 {
		return r.Files[0].Id, nil
	}

	folder := &drive.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
		Parents:  []string{parentID},
	}
	created, err := d.srv.Files.Create(folder).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("create subfolder: %w", err)
	}

	return created.Id, nil
}
