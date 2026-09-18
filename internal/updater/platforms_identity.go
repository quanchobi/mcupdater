package updater

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// IdentifyMod uses the actual installed bytes, never a filename heuristic.
func (c *Client) IdentifyMod(ctx context.Context, filename string) (Mod, error) {
	basename := filepath.Base(filename)
	if !safeJarName(basename) {
		return Mod{}, fmt.Errorf("installed mod must have a safe JAR basename: %q", basename)
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return Mod{}, err
	}
	if !info.Mode().IsRegular() {
		return Mod{}, fmt.Errorf("installed mod %q is not a regular file", filename)
	}
	file, err := os.Open(filename)
	if err != nil {
		return Mod{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return Mod{}, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return Mod{}, fmt.Errorf("installed mod changed while opening %q", filename)
	}
	hash, filteredLength, err := modIdentityHash(ctx, file)
	if err != nil {
		return Mod{}, fmt.Errorf("hash installed mod: %w", err)
	}
	current, err := file.Stat()
	if err != nil {
		return Mod{}, err
	}
	if current.Size() != opened.Size() || !current.ModTime().Equal(opened.ModTime()) {
		return Mod{}, fmt.Errorf("installed mod changed while hashing: %q", filename)
	}
	mod, err := c.identifyModrinth(ctx, hash)
	if err == nil {
		mod.File = basename
		return mod, nil
	}
	if !errors.Is(err, ErrUnavailable) {
		return Mod{}, err
	}
	if strings.TrimSpace(c.CurseForgeKey) == "" {
		return Mod{}, fmt.Errorf("JAR was not identified on Modrinth; set CURSEFORGE_API_KEY to enable CurseForge fingerprint identification")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return Mod{}, err
	}
	fingerprint, err := curseForgeFingerprint(ctx, file, filteredLength)
	if err != nil {
		return Mod{}, fmt.Errorf("fingerprint installed mod: %w", err)
	}
	current, err = file.Stat()
	if err != nil {
		return Mod{}, err
	}
	if current.Size() != opened.Size() || !current.ModTime().Equal(opened.ModTime()) {
		return Mod{}, fmt.Errorf("installed mod changed during identification: %q", filename)
	}
	mod, err = c.identifyCurseForge(ctx, fingerprint, hash)
	if err != nil {
		return Mod{}, err
	}
	mod.File = basename
	return mod, nil
}

func (c *Client) identifyModrinth(ctx context.Context, hash string) (Mod, error) {
	// The documented multiple=true response can be an array; deployed v2 also
	// returns a single object. Never choose between different reported projects.
	var raw json.RawMessage
	if err := c.GetJSON(ctx, modrinthAPI+"/version_file/"+hash+"?algorithm=sha1&multiple=true", &raw); err != nil {
		return Mod{}, modNotFound(err, "identify Modrinth JAR")
	}
	raw = bytes.TrimSpace(raw)
	var versions []modrinthVersion
	if len(raw) != 0 && raw[0] == '{' {
		var version modrinthVersion
		if err := json.Unmarshal(raw, &version); err != nil {
			return Mod{}, fmt.Errorf("malformed Modrinth hash response: %w", err)
		}
		versions = []modrinthVersion{version}
	} else if len(raw) != 0 && raw[0] == '[' {
		if err := json.Unmarshal(raw, &versions); err != nil {
			return Mod{}, fmt.Errorf("malformed Modrinth hash response: %w", err)
		}
	} else {
		return Mod{}, fmt.Errorf("malformed Modrinth hash response")
	}
	projectID := ""
	for _, version := range versions {
		if version.ID == "" || version.ProjectID == "" || version.Files == nil {
			return Mod{}, fmt.Errorf("malformed Modrinth hash match")
		}
		matched := false
		for _, file := range version.Files {
			if strings.EqualFold(file.Hashes["sha1"], hash) {
				matched = true
				break
			}
		}
		if !matched {
			return Mod{}, fmt.Errorf("Modrinth hash response does not match the installed JAR")
		}
		if projectID != "" && projectID != version.ProjectID {
			return Mod{}, fmt.Errorf("installed JAR has ambiguous Modrinth project identities")
		}
		projectID = version.ProjectID
	}
	if projectID == "" {
		return Mod{}, fmt.Errorf("JAR was not identified on Modrinth: %w", ErrUnavailable)
	}
	project, err := c.modrinthProject(ctx, projectID)
	if err != nil {
		return Mod{}, err
	}
	if project.ID != projectID {
		return Mod{}, fmt.Errorf("Modrinth identity returned an unrelated project")
	}
	if project.ProjectType != "mod" {
		return Mod{}, fmt.Errorf("Modrinth hash identifies a %s, not a mod", project.ProjectType)
	}
	mandatory := true
	return Mod{Name: project.Title, Platform: "modrinth", ProjectID: project.ID, Mandatory: &mandatory}, nil
}

func (c *Client) identifyCurseForge(ctx context.Context, fingerprint uint32, sha1Hash string) (Mod, error) {
	var response struct {
		Data *struct {
			ExactMatches []struct {
				ID   uint32         `json:"id"`
				File curseForgeFile `json:"file"`
			} `json:"exactMatches"`
		} `json:"data"`
	}
	body := struct {
		Fingerprints []uint32 `json:"fingerprints"`
	}{[]uint32{fingerprint}}
	if err := c.curseForgeJSON(ctx, http.MethodPost, "/fingerprints/432", body, &response); err != nil {
		return Mod{}, modNotFound(err, "identify CurseForge JAR")
	}
	if response.Data == nil || response.Data.ExactMatches == nil {
		return Mod{}, fmt.Errorf("malformed CurseForge fingerprint response")
	}
	var projectID uint32
	for _, match := range response.Data.ExactMatches {
		file := match.File
		if match.ID == 0 || file.ID == 0 || file.ModID != match.ID || file.GameID != 432 || file.Fingerprint == nil {
			return Mod{}, fmt.Errorf("malformed CurseForge exact fingerprint match")
		}
		if *file.Fingerprint != fingerprint {
			return Mod{}, fmt.Errorf("CurseForge exact match returned a different fingerprint")
		}
		// Fingerprints are non-cryptographic. A supplied SHA1 must also agree,
		// otherwise this is a collision or a locally modified JAR, not identity.
		collision := false
		for _, hash := range file.Hashes {
			if hash.Algorithm == 1 {
				if !modHash(hash.Value, 20) {
					return Mod{}, fmt.Errorf("malformed CurseForge identity SHA1")
				}
				if !strings.EqualFold(hash.Value, sha1Hash) {
					collision = true
				}
			}
		}
		if collision {
			continue
		}
		if projectID != 0 && projectID != match.ID {
			return Mod{}, fmt.Errorf("installed JAR has ambiguous CurseForge project identities")
		}
		projectID = match.ID
	}
	if projectID == 0 {
		return Mod{}, fmt.Errorf("JAR was not identified by an exact CurseForge fingerprint: %w", ErrUnavailable)
	}
	project, err := c.curseForgeProject(ctx, projectID)
	if err != nil {
		return Mod{}, err
	}
	mandatory := true
	return Mod{Name: project.Name, Platform: "curseforge", ProjectID: strconv.FormatUint(uint64(project.ID), 10), Mandatory: &mandatory}, nil
}

func curseForgeWhitespace(value byte) bool {
	return value == 9 || value == 10 || value == 13 || value == 32
}

// The protocol requires SHA1. Count the filtered length in the same pass so
// CurseForge's length-seeded MurmurHash2 can be streamed without retaining a JAR.
func modIdentityHash(ctx context.Context, reader io.Reader) (string, uint64, error) {
	digest := sha1.New()
	var length uint64
	var buffer [32 * 1024]byte
	for {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}
		count, err := reader.Read(buffer[:])
		if count > 0 {
			_, _ = digest.Write(buffer[:count])
			for _, value := range buffer[:count] {
				if !curseForgeWhitespace(value) {
					length++
				}
			}
		}
		if err == io.EOF {
			return hex.EncodeToString(digest.Sum(nil)), length, nil
		}
		if err != nil {
			return "", 0, err
		}
	}
}

// CurseForge uses MurmurHash2 with seed 1, after removing only ASCII bytes
// 9, 10, 13 and 32. Input length is the filtered byte count, not file size.
func curseForgeFingerprint(ctx context.Context, reader io.Reader, length uint64) (uint32, error) {
	const multiplier uint32 = 0x5bd1e995
	hash := uint32(1) ^ uint32(length)
	var word uint32
	var shift uint
	var consumed uint64
	var buffer [32 * 1024]byte
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		count, err := reader.Read(buffer[:])
		for _, value := range buffer[:count] {
			if curseForgeWhitespace(value) {
				continue
			}
			consumed++
			word |= uint32(value) << shift
			shift += 8
			if shift == 32 {
				word *= multiplier
				word ^= word >> 24
				word *= multiplier
				hash = hash*multiplier ^ word
				word, shift = 0, 0
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
	}
	if consumed != length {
		return 0, fmt.Errorf("JAR changed between fingerprint passes")
	}
	if shift != 0 {
		hash ^= word
		hash *= multiplier
	}
	hash ^= hash >> 13
	hash *= multiplier
	hash ^= hash >> 15
	return hash, nil
}
