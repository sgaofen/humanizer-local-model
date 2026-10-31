//go:build linux

package launcher

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func writeTarGz(t *testing.T, path string, entries map[string]string, modes map[string]int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		mode := int64(0o644)
		if m, ok := modes[name]; ok {
			mode = m
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractTarGz(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "pkg.tar.gz")
	writeTarGz(t, pkg, map[string]string{"humanizer": "#!/bin/sh\n", "engine/cpu/llama-server": "bin"},
		map[string]int64{"humanizer": 0o755})
	out := filepath.Join(dir, "out")
	if err := extractTarGz(pkg, out); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(out, "humanizer"))
	if err != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("humanizer mode = %v, err = %v", st.Mode(), err)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "engine/cpu/llama-server")); string(b) != "bin" {
		t.Fatalf("engine file = %q", b)
	}
}

func TestExtractTarGzRejectsEscape(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "evil.tar.gz")
	writeTarGz(t, pkg, map[string]string{"../evil": "x"}, nil)
	if err := extractTarGz(pkg, filepath.Join(dir, "out")); err == nil {
		t.Fatal("expected error for ../ entry")
	}
	if _, err := os.Stat(filepath.Join(dir, "evil")); !os.IsNotExist(err) {
		t.Fatal("escape file was written")
	}
}

func TestExtractTarGzDotSlashEntries(t *testing.T) {
	// 真实 build_linux.sh 用 tar -czf ... -C stage . 打包,第一条是 ./,
	// 之后每条都带 ./ 前缀;解压必须接受(回归:曾被当成空路径拒绝)。
	dir := t.TempDir()
	pkg := filepath.Join(dir, "real.tar.gz")
	f, err := os.Create(pkg)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range []struct {
		name string
		mode int64
		typ  byte
		body string
	}{
		{"./", 0o755, tar.TypeDir, ""},
		{"./humanizer", 0o777, tar.TypeReg, "bin"},
		{"./engine/", 0o755, tar.TypeDir, ""},
		{"./engine/cpu/", 0o755, tar.TypeDir, ""},
		{"./engine/cpu/llama-server", 0o755, tar.TypeReg, "srv"},
		{"./engine/cpu/libggml.so", 0o644, tar.TypeSymlink, ""},
	} {
		h := &tar.Header{Name: e.name, Mode: e.mode, Typeflag: e.typ, Size: int64(len(e.body))}
		if e.typ == tar.TypeSymlink {
			h.Linkname = "llama-server"
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	if err := extractTarGz(pkg, out); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(out, "humanizer"))
	if err != nil || st.Mode().Perm() != 0o755 { // 0777 必须被压到 0755
		t.Fatalf("humanizer mode = %v err = %v", st.Mode(), err)
	}
	if _, err := os.Lstat(filepath.Join(out, "engine/cpu/libggml.so")); err != nil {
		t.Fatalf("in-tree symlink missing: %v", err)
	}
}

func TestExtractTarGzRejectsBadSymlink(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "evil-link.tar.gz")
	f, err := os.Create(pkg)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "lib.so", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "../../etc/passwd"}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractTarGz(pkg, filepath.Join(dir, "out")); err == nil {
		t.Fatal("expected error for symlink escaping the package")
	}
}

func TestPackageVerifierLinux(t *testing.T) {
	dir := t.TempDir()
	pkg := filepath.Join(dir, "Humanizer-9.9.9-linux-x64-portable.tar.gz")
	writeTarGz(t, pkg, map[string]string{"humanizer": "x"}, nil)
	if err := packageVerifier(pkg)(pkg); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(dir, "Humanizer-9.9.9-linux-x64.AppImage")
	if err := os.WriteFile(img, []byte("\x7fELFfake"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := packageVerifier(img)(img); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.AppImage")
	if err := os.WriteFile(bad, []byte("<html>nope</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := packageVerifier(bad)(bad); err == nil {
		t.Fatal("expected error for non-ELF AppImage")
	}
}

func TestDetectInstallAppImage(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "Humanizer-0.3.4-linux-x64.AppImage")
	if err := os.WriteFile(img, []byte("\x7fELF"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// 必须证明正在跑的程序就在这个 AppImage 的挂载目录里:
	// 只设 APPIMAGE(继承自别的 AppImage 的终端)不算数。
	t.Setenv("APPIMAGE", img)
	if in := detectInstall(); in.Kind == "linux_appimage" {
		t.Fatalf("stale APPIMAGE env trusted: %+v", in)
	}
	t.Setenv("APPDIR", filepath.Dir(exe))
	in := detectInstall()
	if in.Kind != "linux_appimage" || in.Path != img || in.Suffix != "-linux-x64.AppImage" || in.Mode != "replace" {
		t.Fatalf("detectInstall = %+v", in)
	}
}
