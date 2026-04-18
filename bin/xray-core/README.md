# xray-core 内置目录说明

用于 GitHub 版本检查的 VMess 代理能力，默认会按如下路径查找二进制：

- `bin/xray-core/<os>-<arch>/xray`
- Windows 下文件名为 `xray.exe`

示例：

- macOS Apple Silicon: `bin/xray-core/darwin-arm64/xray`
- Linux x86_64: `bin/xray-core/linux-amd64/xray`
- Windows x86_64: `bin/xray-core/windows-amd64/xray.exe`

也可以通过环境变量覆盖路径：

- `GITHUB_XRAY_BIN=./bin/xray-core/darwin-arm64/xray`

> 注意：本仓库不直接提交上游二进制，请在发布打包阶段把对应平台的 `xray-core` 放入上述目录。
