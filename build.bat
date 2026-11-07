@echo off
setlocal
if /I "%~1"=="--help" goto help
if /I "%~1"=="-h" goto help
if "%~1"=="/?" goto help
for /f %%A in ('go env GOOS') do set "HOST_OS=%%A"
for /f %%A in ('go env GOARCH') do set "HOST_ARCH=%%A"
set "FAILED=0"
set "ALL_MODE=0"

if /I "%~1"=="--all" if "%~2"=="" goto build_all
if not "%~1"=="" goto usage

call :build_target "%HOST_OS%" "%HOST_ARCH%" "openscript"
exit /b %ERRORLEVEL%

:build_all
set "ALL_MODE=1"
if not exist dist\compressed mkdir dist\compressed
for /f "tokens=1,2 delims=/" %%A in ('go tool dist list') do (
    call :should_build %%A %%B
    if not errorlevel 1 call :package_target %%A %%B
)
if not "%FAILED%"=="0" exit /b 1
exit /b 0

:should_build
if /I "%~1"=="windows" goto supported_os
if /I "%~1"=="linux" goto supported_os
if /I "%~1"=="darwin" goto supported_os
if /I "%~1"=="dragonfly" goto supported_os
if /I "%~1"=="freebsd" goto supported_os
if /I "%~1"=="netbsd" goto supported_os
if /I "%~1"=="openbsd" goto supported_os
exit /b 1

:supported_os
if /I "%~2"=="amd64" exit /b 0
if /I "%~2"=="arm64" exit /b 0
if /I "%~2"=="386" exit /b 0
if /I "%~2"=="arm" exit /b 0
if /I "%~2"=="riscv64" exit /b 0
exit /b 1

:package_target
set "TARGET_OS=%~1"
set "TARGET_ARCH=%~2"
set "STAGE=dist\compressed\.stage-%TARGET_OS%-%TARGET_ARCH%"
if /I "%TARGET_OS%"=="windows" (
    set "ARCHIVE=dist\compressed\openscript-%TARGET_OS%-%TARGET_ARCH%.zip"
) else (
    set "ARCHIVE=dist\compressed\openscript-%TARGET_OS%-%TARGET_ARCH%.tar.gz"
)
if exist "%STAGE%" rmdir /S /Q "%STAGE%"
mkdir "%STAGE%"
call :build_target "%TARGET_OS%" "%TARGET_ARCH%" "%STAGE%\openscript"
if errorlevel 1 goto package_failed
xcopy /E /I /Y /Q "lib" "%STAGE%\lib" >nul
if errorlevel 1 goto package_failed
if /I "%TARGET_OS%"=="windows" (
    powershell -NoProfile -Command "Compress-Archive -Path '%STAGE%\*' -DestinationPath '%ARCHIVE%' -Force"
) else (
    tar -czf "%ARCHIVE%" -C "%STAGE%" .
)
if errorlevel 1 goto package_failed
rmdir /S /Q "%STAGE%"
echo Packaged %TARGET_OS%/%TARGET_ARCH% to %ARCHIVE%.
exit /b 0

:package_failed
set "FAILED=1"
if exist "%STAGE%" rmdir /S /Q "%STAGE%"
exit /b 1

:build_target
set "TARGET_OS=%~1"
set "TARGET_ARCH=%~2"
set "OUTPUT=%~3"
set "RESOURCE=main\rsrc_windows_%TARGET_ARCH%.syso"
set "TARGET_FOUND=0"
for /f %%T in ('go tool dist list') do if /I "%%T"=="%TARGET_OS%/%TARGET_ARCH%" set "TARGET_FOUND=1"
if "%TARGET_FOUND%"=="0" (
    echo Skipping unsupported target %TARGET_OS%/%TARGET_ARCH%.
    exit /b 0
)
if /I "%TARGET_OS%"=="windows" (
    set "GOOS=%HOST_OS%"
    set "GOARCH=%HOST_ARCH%"
    if exist "%RESOURCE%" del "%RESOURCE%"
    go run github.com/akavel/rsrc@v0.10.2 -ico docs\openscript.ico -o "%RESOURCE%" -arch "%TARGET_ARCH%"
    if errorlevel 1 (
        set "FAILED=1"
        set "BUILD_RC=1"
        goto target_cleanup
    )
)

if /I "%TARGET_OS%"=="windows" if /I not "%OUTPUT:~-4%"==".exe" set "OUTPUT=%OUTPUT%.exe"

set "GOOS=%TARGET_OS%"
set "GOARCH=%TARGET_ARCH%"
if "%ALL_MODE%"=="1" set "CGO_ENABLED=0"
echo Building %TARGET_OS%/%TARGET_ARCH% to %OUTPUT%...
if "%ALL_MODE%"=="1" (
    set "CGO_ENABLED=0"
    go build -trimpath -ldflags="-s -w" -o "%OUTPUT%" ./main
) else (
    go build -o "%OUTPUT%" ./main
)
set "BUILD_RC=%ERRORLEVEL%"
if not "%BUILD_RC%"=="0" set "FAILED=1"

:target_cleanup
if exist "%RESOURCE%" del "%RESOURCE%"
exit /b %BUILD_RC%

:usage
echo Usage: build.bat [--all]
echo Run "build.bat --help" for details.
exit /b 2

:help
echo(Usage: build.bat [--all ^| --help ^| -h ^| /?]
echo(
echo(Build the current operating system and architecture into openscript.exe.
echo(Windows builds use the .exe extension.
echo(
echo(Options:
echo(  --all    Build optimized Windows, Linux, macOS, and BSD releases for amd64, arm64, 386, arm, and riscv64.
echo(           Packages are written to dist\compressed as .zip or .tar.gz with lib\ included.
echo(  --help   Show this help and exit.
echo(  -h       Show this help and exit.
echo(  /?       Show this help and exit.
exit /b 0