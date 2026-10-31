@echo off
go build -gcflags="-m" ./main > build.tmp 2>&1
set RC=%ERRORLEVEL%
findstr /C:"escapes to heap" /C:"moved to heap" /C:"leaking param" build.tmp > build.txt
del build.tmp
exit /b %RC%