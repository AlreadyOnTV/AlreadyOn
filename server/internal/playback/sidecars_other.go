//go:build !linux && !darwin

package playback

import "os"

const sidecarsSupported = false

func openSidecar(*os.Root, string) (*os.File, error) { return nil, errInvalidSidecar }
func sidecarFileID(os.FileInfo) string               { return "" }
