@echo off
chcp 65001 >nul
echo ============================================================
echo   DeepSeek V4 + Codex Desktop + CLIProxyAPI 一键配置脚本
echo ============================================================
echo.

REM === Step 1: 检查 CLIProxyAPI 是否已安装 ===
where cliproxyapi >nul 2>&1
if %ERRORLEVEL% NEQ 0 (
    echo [1/4] CLIProxyAPI 未安装。
    echo.
    echo 请先下载 CLIProxyAPI:
    echo   GitHub Releases: https://github.com/router-for-me/CLIProxyAPI/releases/latest
    echo   下载 windows_amd64.zip，解压后将 cliproxyapi.exe 放到 PATH 目录
    echo   或直接放到当前文件夹。
    echo.
    pause
    exit /b 1
)
echo [1/4] CLIProxyAPI 已就绪 ✓

REM === Step 2: 复制 config.yaml 到用户目录 ===
if not exist "%USERPROFILE%\.cli-proxy-api" mkdir "%USERPROFILE%\.cli-proxy-api"
copy /Y "%~dp0config.yaml" "%USERPROFILE%\.cli-proxy-api\config.yaml" >nul
echo [2/4] config.yaml 已复制到 %%USERPROFILE%%\.cli-proxy-api\ ✓

REM === Step 3: 复制 Codex 配置文件 ===
if not exist "%USERPROFILE%\.codex" mkdir "%USERPROFILE%\.codex"
copy /Y "%~dp0codex-config.toml" "%USERPROFILE%\.codex\config.toml" >nul
copy /Y "%~dp0codex-auth.json" "%USERPROFILE%\.codex\auth.json" >nul
echo [3/4] Codex 配置文件已复制到 %%USERPROFILE%%\.codex\ ✓

REM === Step 4: 提示用户修改 API Key ===
echo [4/4] 配置文件已部署完毕 ✓
echo.
echo ============================================================
echo   接下来请手动完成以下步骤:
echo ============================================================
echo.
echo  1. 编辑 %USERPROFILE%\.cli-proxy-api\config.yaml
echo     把 api-key 替换为你的 DeepSeek API Key
echo     (在 https://platform.deepseek.com/api_keys 获取)
echo.
echo  2. 启动 CLIProxyAPI:
echo     cliproxyapi --config "%USERPROFILE%\.cli-proxy-api\config.yaml"
echo.
echo  3. 新开一个终端，启动 Codex:
echo     codex --profile ds-pro      (使用 DeepSeek V4 Pro)
echo     codex --profile ds-flash    (使用 DeepSeek V4 Flash)
echo     codex                        (默认使用 V4 Pro)
echo.
echo ============================================================
pause
