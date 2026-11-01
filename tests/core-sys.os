import {
	OS,
	ARCH,
	BITNESS,
	CPU_COUNT,
	GO_VERSION,
	PID,
	PATH_SEP,
	PATH_LIST_SEP,
	LINE_SEP,
	IS_WINDOWS,
	IS_LINUX,
	IS_MACOS,
	cwd,
	executable,
	hostname,
	homeDir,
	tempDir,
	env
} from "core:sys";
import { println } from "core:io";

if (OS == "" || ARCH == "" || (BITNESS != 32 && BITNESS != 64) || CPU_COUNT < 1 || GO_VERSION == "" || PID < 1) {
	println("FAIL: runtime information");
}
if (PATH_SEP == "" || PATH_LIST_SEP == "" || LINE_SEP == "") {
	println("FAIL: platform separators");
}
if ((IS_WINDOWS ? 1 : 0) + (IS_LINUX ? 1 : 0) + (IS_MACOS ? 1 : 0) > 1) {
	println("FAIL: OS predicates overlap");
}
if (cwd() == "" || executable() == "" || hostname() == "" || homeDir() == "" || tempDir() == "") {
	println("FAIL: system location helpers");
}
if (env("OPEN_SCRIPT_TEST_MISSING_ENV") != null) {
	println("FAIL: missing environment variable");
}

println("core sys ok");