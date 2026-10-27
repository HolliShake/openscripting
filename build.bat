@echo off
go build -gcflags="-m" ./main > build.txt 2>&1
exit /b %ERRORLEVEL%
