package documents

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// ConvertToPDF converts docx bytes to PDF using a headless LibreOffice/soffice
// process. A throwaway user profile dir is used so concurrent conversions don't
// clash. If soffice is unavailable, the error is returned to the caller.
func ConvertToPDF(ctx context.Context, sofficeBin string, docx []byte) ([]byte, error) {
	tmp, err := os.MkdirTemp("", "hrcrm-doc-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	inPath := filepath.Join(tmp, "doc.docx")
	if err := os.WriteFile(inPath, docx, 0o600); err != nil {
		return nil, err
	}

	profile := filepath.Join(tmp, "profile")
	cmd := exec.CommandContext(ctx, sofficeBin,
		"--headless", "--norestore", "--nolockcheck", "--nodefault",
		"-env:UserInstallation=file://"+profile,
		"--convert-to", "pdf", "--outdir", tmp, inPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("libreoffice convert failed: %w: %s", err, string(out))
	}

	pdfPath := filepath.Join(tmp, "doc.pdf")
	pdf, err := os.ReadFile(pdfPath)
	if err != nil {
		return nil, fmt.Errorf("pdf not produced: %w", err)
	}
	return pdf, nil
}

// SofficeAvailable reports whether the configured binary can be found.
func SofficeAvailable(sofficeBin string) bool {
	_, err := exec.LookPath(sofficeBin)
	return err == nil
}
