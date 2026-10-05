@echo off
rem Trans 一键构建：图标 -> exe(图标+版本) -> 安装包
cd /d "%~dp0"

set VERSION=0.3.1

echo [1/5] 生成图标...
go run ./tools/makeicon || goto :fail

echo [2/5] 生成版本资源(trans.exe)...
go run ./tools/mkversion -icon installer\trans.ico -o resource.syso || goto :fail

echo [3/5] 构建 trans.exe...
go build -trimpath -ldflags "-H=windowsgui -s -w" -o trans.exe . || goto :fail

echo [4/5] 准备安装器 payload...
if not exist installer\setup\payload mkdir installer\setup\payload
copy /y trans.exe installer\setup\payload\trans.exe >nul || goto :fail
go run ./tools/mkversion -spec installer\setup\versioninfo.json -icon installer\trans.ico -o installer\setup\resource.syso || goto :fail

echo [5/5] 构建安装包...
if not exist dist mkdir dist
go build -trimpath -ldflags "-s -w" -o dist\Trans-%VERSION%-setup.exe ./installer/setup || goto :fail

echo.
echo 构建完成:
echo   trans.exe
echo   dist\Trans-%VERSION%-setup.exe
pause
exit /b 0

:fail
echo 构建失败
pause
exit /b 1
