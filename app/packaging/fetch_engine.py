#!/usr/bin/env python3
"""下载钉死版本的 llama.cpp 官方二进制,校验 sha256,整理成启动器认识的目录:

  macos   → <out>/metal/{llama-server, *.dylib, LICENSE}
  windows → <out>/cpu/    {llama-server.exe, *.dll, LICENSE}
            <out>/vulkan/ {...}
            <out>/cuda/   {... + cudart/cublas 运行库}
  linux   → <out>/cpu/    {llama-server, *.so, LICENSE}
            <out>/vulkan/ {...}(Vulkan 覆盖 AMD/Intel/NVIDIA;不带 CUDA,包体积小一个量级)

只保留 llama-server 及其依赖的动态库,其他命令行工具丢掉(省体积)。
用法:python3 packaging/fetch_engine.py --target macos --out dist/engine
"""
import argparse
import hashlib
import json
import os
import shutil
import sys
import tarfile
import tempfile
import urllib.request
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))
LAYOUT = {
    "macos": [("metal", ["macos-arm64"])],
    "windows": [
        ("cpu", ["win-cpu-x64"]),
        ("vulkan", ["win-vulkan-x64"]),
        ("cuda", ["win-cuda-12.4-x64", "win-cudart-12.4-x64"]),
    ],
    "linux": [
        ("cpu", ["linux-cpu-x64"]),
        ("vulkan", ["linux-vulkan-x64"]),
    ],
}


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def fetch(lock, key, cache):
    a = lock["assets"][key]
    dst = os.path.join(cache, a["file"])
    if os.path.exists(dst) and sha256(dst) == a["sha256"]:
        print(f"  缓存命中 {a['file']}")
        return dst
    url = f"{lock['base_url']}/{a['file']}"
    print(f"  下载 {url}")
    tmp = dst + ".tmp"
    with urllib.request.urlopen(url, timeout=120) as r, open(tmp, "wb") as f:
        shutil.copyfileobj(r, f, 1 << 20)
    got = sha256(tmp)
    if got != a["sha256"]:
        os.remove(tmp)
        sys.exit(f"sha256 不符:{a['file']}\n  期望 {a['sha256']}\n  实际 {got}")
    os.replace(tmp, dst)
    return dst


def extract(archive, into):
    if archive.endswith(".zip"):
        with zipfile.ZipFile(archive) as z:
            z.extractall(into)
    else:
        with tarfile.open(archive) as t:
            try:
                t.extractall(into, filter="data")  # Python ≥3.12
            except TypeError:
                sys.exit("需要 Python ≥3.12(安全的 tar 解压过滤),请升级 Python 后再打包")


# llama-server 用不到的工具实现库,不进包(bench/cli/perplexity/tts 等)。
UNUSED_IMPL = ("-bench", "-cli", "-perplexity", "-tts", "-quantize", "-fit-params", "-completion", "-batched-bench")


def wanted(name):
    low = name.lower()
    if low in ("llama-server", "llama-server.exe") or low.startswith("license"):
        return True
    if any(x in low for x in UNUSED_IMPL):
        return False
    return low.endswith(".dylib") or low.endswith(".dll") or low.endswith(".so") or ".so." in low


def collect(src_root, dest):
    os.makedirs(dest, exist_ok=True)
    n = 0
    for root, _, files in os.walk(src_root):
        for fn in files:
            if not wanted(fn):
                continue
            s, d = os.path.join(root, fn), os.path.join(dest, fn)
            if os.path.lexists(d):
                os.remove(d)
            if os.path.islink(s):  # mac 的 libggml.dylib -> libggml.0.dylib 这类软链接原样保留
                os.symlink(os.readlink(s), d)
            else:
                shutil.copy2(s, d)
            n += 1
    return n


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--target", choices=sorted(LAYOUT), required=True)
    ap.add_argument("--out", required=True)
    ap.add_argument("--cache", default=os.path.join(HERE, "..", "dist", "engine-cache"))
    args = ap.parse_args()
    with open(os.path.join(HERE, "llama-cpp.lock.json"), encoding="utf-8") as f:
        lock = json.load(f)
    os.makedirs(args.cache, exist_ok=True)
    print(f"llama.cpp {lock['tag']} ({lock['commit']}) -> {args.out}")
    for name, keys in LAYOUT[args.target]:
        dest = os.path.join(args.out, name)
        shutil.rmtree(dest, ignore_errors=True)
        total = 0
        for key in keys:
            arc = fetch(lock, key, args.cache)
            with tempfile.TemporaryDirectory() as tmp:
                extract(arc, tmp)
                total += collect(tmp, dest)
        server = os.path.join(dest, "llama-server.exe" if args.target == "windows" else "llama-server")
        if not os.path.exists(server):
            sys.exit(f"{name}: 压缩包里没找到 llama-server")
        with open(os.path.join(dest, "ENGINE_VERSION.txt"), "w") as f:
            f.write(f"llama.cpp {lock['tag']} {lock['commit']} ({', '.join(keys)})\n")
        print(f"  {name}: {total} 个文件")


if __name__ == "__main__":
    main()
