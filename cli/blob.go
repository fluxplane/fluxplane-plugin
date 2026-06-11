package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	sdkhost "github.com/fluxplane/fluxplane-plugin/host"
	"github.com/fluxplane/fluxplane-plugin/management"
)

// blobPutter is the optional backend capability behind `blob put` — the local
// backend implements it; remote backends may not.
type blobPutter interface {
	BlobPut(plugin, instance string, req sdkhost.BlobWriteRequest) (sdkhost.BlobRef, error)
}

func newBlobCommand(backend management.Backend) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "blob",
		Short: "Manage plugin blob stores",
	}
	cmd.AddCommand(newBlobPutCommand(backend))
	return cmd
}

func newBlobPutCommand(backend management.Backend) *cobra.Command {
	var instance, ref, mediaType string
	var overwrite bool
	cmd := &cobra.Command{
		Use:   "put PLUGIN FILE",
		Short: "Store a local file in a plugin's blob store and print its blob_ref",
		Long: "Copies a local file into PLUGIN's blob store so large payloads ride as a blob_ref " +
			"instead of inline content_bytes (which can exceed the OS argv limit). Example: " +
			"`fluxplane-plugin blob put slack ./screenshot.png` then " +
			"`operation invoke slack slack.file.upload --arg channel=#x --arg blob_ref=<ref>`.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := backendRequired(backend); err != nil {
				return err
			}
			store, ok := backend.(blobPutter)
			if !ok {
				return fmt.Errorf("fluxplane-plugin: this backend exposes no blob store")
			}
			content, err := os.ReadFile(args[1])
			if err != nil {
				return err
			}
			blob, err := store.BlobPut(args[0], instance, sdkhost.BlobWriteRequest{
				Ref:       ref,
				Content:   content,
				Filename:  filepath.Base(args[1]),
				MediaType: mediaType,
				Overwrite: overwrite,
				Metadata:  map[string]string{"source": "blob put", "origin_path": args[1]},
			})
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), blob)
		},
	}
	cmd.Flags().StringVar(&instance, "instance", defaultInstance(), "plugin instance")
	cmd.Flags().StringVar(&ref, "ref", "", "explicit blob ref (default: derived from the filename)")
	cmd.Flags().StringVar(&mediaType, "media-type", "", "blob media type (e.g. image/png)")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace an existing blob with the same ref")
	return cmd
}
