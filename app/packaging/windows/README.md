# 打包成 Windows EXE:完整步骤

Windows 上的产物有两个,都是 `.exe`(便携版外面再套一层 zip):

| 产物 | 说明 |
|---|---|
| `dist/Humanizer-<版本>-windows-x64-setup.exe` | Inno Setup 安装包,双击安装,按用户安装(不要管理员权限),带开始菜单和桌面快捷方式、可选卸载 |
| `dist/Humanizer-<版本>-windows-x64-portable.zip` | 便携版,解压即用,不写注册表。里面是 `Humanizer.exe` + `engine/` |

两个 EXE 里的启动器是同一个二进制(`-H=windowsgui`,双击不弹黑色控制台窗口);
区别只在有没有安装器。

## 0. 需要装好的东西

| 工具 | 版本 | 装法 |
|---|---|---|
| Go | ≥ 1.24.1(go.mod 里写的;CI 用 1.27.1) | <https://go.dev/dl/> 下 `go1.xx.x.windows-amd64.zip`,解压到 `C:\Go`,把 `C:\Go\bin` 加进 PATH。验证:`go version` |
| Python | ≥ 3.8(只有 `fetch_engine.py` 要) | <https://www.python.org/downloads/>,装时勾 "Add python.exe to PATH"。验证:`python --version` |
| Git for Windows | 任意近期版本 | <https://git-scm.com/download/win>。构建脚本是 bash(`build_win.sh`),没装 Git 就没有 bash |
| Inno Setup | 6.x | <https://jrsoftware.org/isdl.php>。默认装到 `C:\Program Files (x86)\Inno Setup 6\` |
| 7-Zip | 任意 | 便携 zip 那一步(`7z a`)要用。<https://www.7-zip.org/> |

脚本里已经处理过的情况:找不到 Inno Setup 会先 `choco install innosetup`;没有 7z 的话
便携 zip 那一步会失败,安装包不受影响。

## 1. 打开 Git Bash,进到 app 目录

```bash
cd /c/path/to/humanizer-local-model/app
```

后面的命令都在这个目录、`app/` 里面跑。脚本都用相对路径(`packaging/…`、`dist/…`),
不在 `app/` 里跑会找不到文件。

## 2. 拉引擎(llama.cpp 官方二进制,约 0.7 GB)

```bash
python packaging/fetch_engine.py --target windows --out dist/engine
```

这个脚本读 `packaging/llama-cpp.lock.json`(钉死版本 `b11335` + 每个包的 sha256),
下载、校验、整理成启动器认识的目录:

```
dist/engine/cpu/     llama-server.exe + DLL + LICENSE
dist/engine/vulkan/  llama-server.exe + DLL + LICENSE
dist/engine/cuda/    llama-server.exe + DLL + cudart/cublas 运行库
```

三个后端都会打进安装包:有 NVIDIA 驱动先试 CUDA,有 Vulkan 运行时再试 Vulkan,最后 CPU。
压缩包缓存在 `dist/engine-cache/`,重跑就命中缓存,不再下载。想换 llama.cpp 版本改 lock
文件里的 `tag`/`commit`/`sha256`。

国内 GitHub 不稳时的办法(脚本只认这四个文件,没有镜像开关):

- 手动下 lock 里列的四个 zip,放到 `dist/engine-cache/` 下,再跑 `fetch_engine.py` ——
  它会校验 sha256,命中就跳过下载。
- 或者挂代理再跑 `python packaging/fetch_engine.py …`。

只想带 CPU 引擎(安装包从 0.6–0.7 GB 降到 50 MB 以内):拉完引擎后删掉 CUDA/Vulkan 目录,
但 `build_win.sh` 第 1 步会检查三个后端都在,所以这种情况要走[「只出一个 EXE」](#7-只出一个-exe不带安装器)
那套手工步骤。

## 3. 编译 + 出包

```bash
bash packaging/windows/build_win.sh 0.3.2
```

版本号换成你要的(纯数字,`主.次.修订`)。脚本依次做:

1. 检查 `dist/engine/{cpu,vulkan,cuda}/llama-server.exe` 都在;
2. `go-winres` 把图标和版本信息(产品名、文件版本)编成 `.syso`,`go build` 自动带上;
3. `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -H=windowsgui -X main.version=0.3.2" -o dist/win/Humanizer.exe .`;
4. 把 `dist/engine` 和 `LICENSE.txt` 复制到 `dist/win/`;
5. 调 Inno Setup 编译安装包(脚本里 `MSYS2_ARG_CONV_EXCL="*"` 是必须的:Git Bash 会把
   `/DAppVersion=…` 当路径改写,ISCC 会以为有两个脚本文件);
6. `7z a -tzip -mx=7` 打便携 zip。

`go-winres` 第一次跑要联网下载(`go run github.com/tc-hib/go-winres@v0.3.3`)。
国内网络可以先设:

```bash
export GOPROXY=https://goproxy.cn,direct
```

## 4. 产物

```
app/dist/Humanizer-0.3.2-windows-x64-setup.exe        安装包
app/dist/Humanizer-0.3.2-windows-x64-portable.zip     便携版
app/dist/win/Humanizer.exe                             单个启动器(调试用)
app/dist/win/engine/{cpu,vulkan,cuda}/llama-server.exe 引擎
```

装完在 `%LOCALAPPDATA%\Programs\Humanizer`(安装器可以改目录),数据和模型在
`%LOCALAPPDATA%\Humanizer` —— 可以在首次运行的网页上改到别的盘,见
[应用 README 的「存储位置」](../../README.md)。

## 5. 冒烟测一下(和 CI 里一样)

```bash
D="$TEMP/hz-test"          # 换个空的临时目录,别去动真的数据目录
rm -rf "$D"; mkdir -p "$D"
dist/win/engine/cpu/llama-server.exe --version
./dist/win/Humanizer.exe --no-browser --data-dir "$D" --port 47801 &
for i in $(seq 1 40); do curl -sf http://127.0.0.1:47801/app/status > "$D/status.json" && break; sleep 0.5; done
cat "$D/status.json"       # backends 里应有 "cpu"
curl -s -X POST -H 'X-Humanizer: 1' http://127.0.0.1:47801/app/quit
```

`backends` 里出现 `"cpu"` 说明启动器找到了随包附带的引擎。Git Bash 里直接跑
`./dist/win/Humanizer.exe` 也能加 `--no-browser`(没有控制台窗口,输出看不到,属正常)。

## 6. 出正式版之前

- **版本号只有一处来源**:`main.version`(`-ldflags -X main.version=…`)。同一个号还要出现在
  `packaging/windows/humanizer.iss` 的 `AppVersion`(脚本用 `/DAppVersion=` 传进去)和
  README / INSTALL 的下载链接里。检查更新只认 GitHub 上 `app-v` 开头的标签。
- **签名**:现在出的包是未签名的,用户第一次打开 SmartScreen 会拦一下。要消掉就加一步
  `signtool sign /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 /f 证书.pfx …`,
  在 `build_win.sh` 之后对 `Humanizer.exe` 和安装包各签一次。OV 证书要等几天、
  EV 证书几天到几周、Azure Trusted Signing 最快。
- **版本号写进资源后改不动**:改了 `VERSION` 要重跑整个脚本,`.syso` 里是编译进去的。

## 7. 只出一个 EXE(不带安装器)

调试时只想看启动器能不能起来,跳过 Inno Setup:

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -H=windowsgui -X main.version=0.3.2-dev" -o dist/win/Humanizer.exe .
cp -R dist/engine dist/win/engine
./dist/win/Humanizer.exe --data-dir "$TEMP/hz-test" --port 47801
```

这样出来的 EXE 依赖同目录下的 `engine/`,得整个目录一起拷走。只带 CPU 引擎的话:

```bash
rm -rf dist/win/engine/vulkan dist/win/engine/cuda
```

启动器发现有 NVIDIA 驱动就会先试 CUDA,没有就试 Vulkan,都没有就用 CPU —— 目录不存在
就当没有这个后端,不用改代码。

## 8. 常见问题

| 现象 | 原因 | 怎么办 |
|---|---|---|
| `缺少 dist/engine/cpu,先跑 fetch_engine.py` | 第 2 步没跑或跑失败了 | 重跑 `fetch_engine.py`;网络不通换镜像 |
| `sha256 不符` | 下载被截断或中间有代理改内容 | 删 `dist/engine-cache/` 里那个包重下 |
| ISCC 报「有两个脚本文件」 | Git Bash 把 `/DAppVersion=…` 改写成了路径 | 脚本里已经设了 `MSYS2_ARG_CONV_EXCL="*"`,别去掉 |
| 安装界面只有英文 | Inno Setup 没装简体中文语言文件 | 设 `INNO_ZH_URL=https://raw.githubusercontent.com/jrsoftware/issrc/6ef32198ef1f7b7b375cd4b6b90896c2a58eb4c2/Files/Languages/ChineseSimplified.isl` 再跑一次,脚本会下载到 ISCC 目录 |
| `7z: command not found`,最后 `ls` 报错 | 没装 7-Zip | 装上,或者手动 `cd dist/win && zip -r ../portable.zip .` |
| `go: downloading …` 卡住 / `proxy.golang.org` 超时 | `go-winres` 要联网 | `export GOPROXY=https://goproxy.cn,direct` |
| 双击 EXE 什么反应都没有 | 引擎或 DLL 缺失,或者被 SmartScreen 拦了 | 先看有没有弹 SmartScreen;再从命令行加 `--no-browser` 跑,看有没有报错;确认 `engine/` 目录跟着 exe 在一起 |
| 换了存储位置后卸载没删掉模型 | 卸载器只问默认目录和 `location.json` 里记的目录 | 手动删;或在网页上「恢复默认」再卸载 |
| 中文在 Windows 控制台里是乱码 | 控制台是 cp1252 | `PYTHONUTF8=1 PYTHONIOENCODING=utf-8 python packaging/fetch_engine.py …`(CI 里就是这么设的) |

## 和 CI 的关系

`.github/workflows/release-app.yml` 在 `windows-2022` 上跑的就是上面这几步
(`fetch_engine.py` → `build_win.sh` → 冒烟 → 上传产物),多了前面两个 job:
Go 单测 + 网页单测 + 端到端,以及 macOS 那条线。本地出包前最好也跑一遍:

```bash
go vet ./... && GOOS=windows go vet ./... && go test ./...
node devtools/webtest.mjs
node devtools/webtest_update.mjs
bash devtools/e2e.sh        # 假引擎 + 假下载源,不加载真模型
```

不用手工出包也行:Actions 页面手动触发 `release-app`(填版本号),或推 `app-v0.3.2`
这样的标签 —— 标签会额外建一个**草稿** Release,需要人去 GitHub 上点发布。