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

if not exist bin mkdir bin
call :build_target "%HOST_OS%" "%HOST_ARCH%" "bin\openscript"
exit /b %ERRORLEVEL%

:build_all
set "ALL_MODE=1"
if not exist bin mkdir bin
type nul > build.txt
for %%O in (windows linux darwin) do call :build_target %%O "%HOST_ARCH%" "bin\openscript-%%O-%HOST_ARCH%"
if not "%FAILED%"=="0" exit /b 1
exit /b 0

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

if /I "%TARGET_OS%"=="windows" set "OUTPUT=%OUTPUT%.exe"

set "GOOS=%TARGET_OS%"
set "GOARCH=%TARGET_ARCH%"
if "%ALL_MODE%"=="1" set "CGO_ENABLED=0"
echo Building %TARGET_OS%/%TARGET_ARCH% to %OUTPUT%...
go build -gcflags="-m" -o "%OUTPUT%" ./main > build.tmp 2>&1
set "BUILD_RC=%ERRORLEVEL%"
if "%BUILD_RC%"=="0" (
    echo [ %TARGET_OS%/%TARGET_ARCH% ]>>build.txt
    findstr /C:"escapes to heap" /C:"moved to heap" /C:"leaking param" build.tmp >> build.txt
) else (
    type build.tmp
    set "FAILED=1"
)
del build.tmp

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
echo(Build the current operating system and architecture into bin\openscript.
echo(Windows builds use the .exe extension.
echo(
echo(Options:
echo(  --all    Build Windows, Linux, and macOS binaries for the current Go architecture.
echo(           Cross-builds disable cgo and write artifacts to bin\.
echo(  --help   Show this help and exit.
echo(  -h       Show this help and exit.
echo(  /?       Show this help and exit.
exit /b 0