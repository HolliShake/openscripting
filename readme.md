# OpenScript

```text
 ██████╗ ██████╗ ███████╗███╗   ██╗███████╗ ██████╗██████╗ ██╗██████╗ ████████╗
██╔═══██╗██╔══██╗██╔════╝████╗  ██║██╔════╝██╔════╝██╔══██╗██║██╔══██╗╚══██╔══╝
██║   ██║██████╔╝█████╗  ██╔██╗ ██║███████╗██║     ██████╔╝██║██████╔╝   ██║
██║   ██║██╔═══╝ ██╔══╝  ██║╚██╗██║╚════██║██║     ██╔══██╗██║██╔═══╝    ██║
╚██████╔╝██║     ███████╗██║ ╚████║███████║╚██████╗██║  ██║██║██║        ██║
 ╚═════╝ ╚═╝     ╚══════╝╚═╝  ╚═══╝╚══════╝ ╚═════╝╚═╝  ╚═╝╚═╝╚═╝        ╚═╝
```

OpenScript is a scripting language built in Go. It compiles `.os` files to bytecode and runs them on a coroutine-based virtual machine. The repository includes the command-line interpreter, built-in and bundled libraries, and regression tests for the language and runtime.

For syntax, semantics, built-in and bundled module APIs, and runnable language examples, use the [OpenScript Language Reference](docs/index.html). The reference is the canonical feature and API guide; this README focuses on building, running, and working on the project.

Run the interpreter with a script path; pass `-help` to see the command-line options and the colored OpenScript banner.

## Requirements

- Go 1.25.5 or newer.

## Build

Install Go 1.25.5 or newer, then build from the repository root. On Windows PowerShell:

```powershell
New-Item -ItemType Directory -Force bin | Out-Null
go build -o bin/openscript.exe ./main
```

On macOS and Linux, the build script creates `bin/openscript`:

```sh
./build.sh
```

The platform build scripts also support:

- `--all` to build Windows, Linux, and macOS binaries for the current Go architecture into `bin/`. Cross-builds disable cgo, so cgo-dependent features are unavailable in those binaries.
- `--release` to build an optimized, stripped executable in `dist/` and copy the bundled libraries into `dist/lib/`.

Windows builds use `build.bat` and produce `bin\openscript.exe`; macOS and Linux use `build.sh`. Windows binaries embed the project icon. Pass `--help` to either build script for its options.

## Run

Scripts use the `.os` extension. Run a script by passing its path with `-f` or `-file`:

```powershell
./bin/openscript.exe -f .\tests\test.os
```

Use `-help` to view command-line usage. Uncaught synchronous errors and unhandled Promise rejections report source locations and stack traces, then exit unsuccessfully.

## Tests

The `tests/` directory contains OpenScript regression programs. Run a focused script with the built executable:

```powershell
./bin/openscript.exe -file .\tests\throw-statement.os
./bin/openscript.exe -file .\tests\core-error.os
```

Run other `.os` files in `tests/` the same way. These scripts cover language syntax, module loading, built-ins, async behavior, and runtime error handling.

## Runtime and JIT

OpenScript can speed up frequently called, two-argument numeric functions with an adaptive native JIT. Once a function has been called 32 times, eligible code may be compiled for the host processor; anything the JIT does not support keeps running in the bytecode interpreter.

The JIT is available on Windows/amd64, Windows/ARM64, Windows/386, Linux/ARMv7, and macOS/ARM64 when built with cgo enabled. Its coverage depends on the processor: the backends handle common numeric operations and forward conditional branches, while support for bitwise operations, remainder, and shifts varies. Loops, relational comparisons, and other unsupported instructions remain interpreted. Builds made with `--all` disable cgo, so cgo-dependent JIT support is not included. On macOS hardened runtime, `MAP_JIT` may require the `com.apple.security.cs.allow-jit` entitlement. Windows/ARM32 is not a supported Go target.

## Project layout

- `main/` contains the Go lexer, parser, compiler, bytecode definitions, virtual machine, and native integrations.
- `lib/` contains bundled OpenScript modules loaded from the executable's `lib/` directory.
- `tests/` contains OpenScript regression scripts and module-loading fixtures.
- `docs/index.html` is the language and API reference.
- `build.bat` and `build.sh` provide platform build, cross-build, and release workflows.

## Contributing

Keep language behavior, regression scripts, and the reference aligned when changing syntax or runtime APIs. Run the relevant scripts from `tests/` after a change, and build the interpreter with the repository's supported Go version.

## License

OpenScript is distributed under the [MIT License](LICENSE).
