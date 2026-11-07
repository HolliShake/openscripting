<div align="center">

<pre align="left">
 ██████╗ ██████╗ ███████╗███╗   ██╗███████╗ ██████╗██████╗ ██╗██████╗ ████████╗
██╔═══██╗██╔══██╗██╔════╝████╗  ██║██╔════╝██╔════╝██╔══██╗██║██╔══██╗╚══██╔══╝
██║   ██║██████╔╝█████╗  ██╔██╗ ██║███████╗██║     ██████╔╝██║██████╔╝   ██║
██║   ██║██╔═══╝ ██╔══╝  ██║╚██╗██║╚════██║██║     ██╔══██╗██║██╔═══╝    ██║
╚██████╔╝██║     ███████╗██║ ╚████║███████║╚██████╗██║  ██║██║██║        ██║
 ╚═════╝ ╚═╝     ╚══════╝╚═╝  ╚═══╝╚══════╝ ╚═════╝╚═╝  ╚═╝╚═╝╚═╝        ╚═╝
</pre>

**A small, fast scripting language written in Go, with async/await, classes, and an adaptive native JIT.**

[![Stars](https://img.shields.io/github/stars/HolliShake/openscripting?style=flat-square)](https://github.com/HolliShake/openscripting/stargazers)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25.5%2B-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev/dl/)
[![Docs](https://img.shields.io/badge/docs-language%20reference-orange?style=flat-square)](https://hollishake.github.io/openscripting/)

[Language Reference](https://hollishake.github.io/openscripting/) ·
[Quick Start](#quick-start) ·
[Examples](#a-quick-tour) ·
[Contributing](#contributing)

</div>

---

## What is OpenScript?

OpenScript compiles `.os` source files to bytecode and runs them on a **coroutine-based virtual machine**. It ships as a single Go-built interpreter with a standard set of core modules (I/O, files, HTTP, JSON, regex, crypto, sockets, and more) plus bundled libraries.

```js
import { println } from "core:io";

const greeting = "Hello, OpenScript!";
println(greeting);
```

## Highlights

- **Familiar, compact syntax**: `fn` functions, `class` with inheritance, destructuring, spread, and switch expressions.
- **Async/await built in**: async functions return promises with `->then` / `->catch` chaining.
- **Coroutine-based VM**: bytecode compiler plus a virtual machine written entirely in Go.
- **Adaptive native JIT**: hot two-argument numeric functions are compiled to machine code after 32 calls. Anything unsupported falls back to the interpreter ([details](#runtime-and-jit)).
- **Batteries included**: `core:io`, `core:file`, `core:json`, `core:regex`, `core:http`, `core:socket`, `core:crypto`, `core:math`, `core:unicode`, and more.
- **Bundled libraries**: `lib:argparse`, `lib:crypto`, `lib:number`, `lib:tui`.
- **Helpful errors**: uncaught errors and unhandled promise rejections print source locations and stack traces.
- **Cross-platform**: build for Windows, Linux, and macOS with one script.

## A quick tour

### Functions and closures

```js
import { println } from "core:io";

var add::fn(left, right) {
    return left + right;
};

var collect::fn(prefix, values...) {
    println(prefix);
    println(values); // variadic arguments arrive as an array
};

println(add(2, 3));
collect("items", 2, 3, 4);
```

### Arrays, objects, and destructuring

```js
import { println } from "core:io";

const person = { name: "Ada", role: "engineer" };
const { name, role: jobTitle } = person;

const values = [3, 8, 11];
const doubled = values->map(fn(value, index) {
    return value * 2 + index;
});

println(name);
println(jobTitle);
println(doubled);
```

### Classes

```js
import { println } from "core:io";

var Animal::class {
    constructor: fn(name) {
        this->name = name;
    },
    speak: fn() {
        return "...";
    }
};

var Dog::class(Animal) {
    constructor: fn(name) {
        super(name);
        this->kind = "dog";
    },
    speak: fn() {
        return "woof";
    }
};

var pet = Dog("Milo");
println(pet->speak());
```

### Async / await

```js
import { println } from "core:io";

var fetchValue = fn() async {
    return 42;
};

var main = fn() async {
    var result = await fetchValue();
    println(result);
};

main();
```

### Switch expressions

```js
import { println } from "core:io";

var status = 404;

var label = switch (status) {
    200, 201 => "success",
    404 => "missing",
    default => "other"
};

println(label);
```

### Loops and error handling

```js
import { println } from "core:io";

loop (let i = 0; i < 3; i++) {
    println(i);
}

try {
    throw "configuration is missing";
} catch (err) {
    println(err);
}
```

> OpenScript uses `->` for member access and `loop` for all loops. See the [Language Reference](https://hollishake.github.io/openscripting/) for the full syntax, operator table, and module APIs.

## Quick start

### 1. Requirements

- Go **1.25.5** or newer

### 2. Clone and build

```sh
git clone https://github.com/HolliShake/openscripting.git
cd openscripting
```

**macOS / Linux**

```sh
./build.sh
```

**Windows (PowerShell)**

```powershell
go build -o openscript.exe ./main
```

Or use `build.bat`, which also embeds the project icon. Running either build script without arguments creates the executable in the repository root (`openscript` on macOS/Linux, `openscript.exe` on Windows).

The build scripts also support:

| Flag     | What it does                                                                                                                                     |
| -------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| `--all`  | Builds optimized archives under `dist/compressed/` for supported Windows, Linux, macOS, and BSD targets. Windows uses ZIP; others use `.tar.gz`. |
| `--help` | Lists all options.                                                                                                                               |

### 3. Run a script

Save the following as `hello.os`:

```js
import { println } from "core:io";

println("Hello, OpenScript!");
```

Then run it:

```sh
./openscript -f hello.os
```

On Windows:

```powershell
.\openscript.exe -f .\hello.os
```

Use `-f` or `-file` to pass a script, and `-help` to see all command-line options along with the colored banner.

## Documentation

| Resource                                                          | What you'll find                                                  |
| ----------------------------------------------------------------- | ----------------------------------------------------------------- |
| [Language Reference](https://hollishake.github.io/openscripting/) | Syntax, semantics, operators, modules, and runnable API examples. |
| [`tests/`](tests/)                                                | Regression programs that double as usage examples.                |
| [`lib/`](lib/)                                                    | Source of the bundled OpenScript libraries.                       |

The reference is the canonical feature and API guide; this README focuses on building, running, and working on the project.

## Standard modules

**Core modules** (import by name, named exports only):

```js
import { println } from "core:io";
```

| Module                                     | Purpose                             |
| ------------------------------------------ | ----------------------------------- |
| `core:io`, `core:file`, `core:path`        | Console I/O, filesystem, paths      |
| `core:math`, `core:number`, `core:unicode` | Numerics and Unicode operations     |
| `core:json`, `core:regex`                  | JSON and regular expressions        |
| `core:http`, `core:socket`                 | HTTP and loopback TCP               |
| `core:crypto`, `core:sys`, `core:error`    | Crypto, host system, error handling |

**Bundled libraries** (loaded from the executable's `lib/` directory):

```js
import { isEven } from "lib:number";
```

| Module         | Purpose                                                   |
| -------------- | --------------------------------------------------------- |
| `lib:argparse` | Command-line argument parsing (`ArgumentParser`)          |
| `lib:number`   | Numeric predicates (`isEven`, `isOdd`, `isPositive`, ...) |
| `lib:crypto`   | CryptoJS-style hashes, HMAC, encoders, PBKDF2, and EvpKDF |
| `lib:tui`      | Terminal colors, prompts, confirmation, and table output  |

## Tests

The `tests/` directory contains OpenScript regression programs covering language syntax, module loading, built-ins, async behavior, and runtime error handling. Run a focused script with the built executable:

```powershell
./openscript.exe -file .\tests\throw-statement.os
./openscript.exe -file .\tests\core-error.os
```

Run other `.os` files in `tests/` the same way.

## Runtime and JIT

OpenScript can speed up frequently called, two-argument numeric functions with an adaptive native JIT. Once a function has been called 32 times, eligible code may be compiled for the host processor; anything the JIT does not support keeps running in the bytecode interpreter.

**Supported platforms** (built with cgo enabled):

| OS      | Architectures     |
| ------- | ----------------- |
| Windows | amd64, ARM64, 386 |
| Linux   | ARMv7             |
| macOS   | ARM64             |

Notes:

- Coverage depends on the processor. The backends handle common numeric operations and forward conditional branches; support for bitwise operations, remainder, and shifts varies.
- Loops, relational comparisons, and other unsupported instructions remain interpreted.
- Builds made with `--all` disable cgo, so native JIT support is not included.
- On macOS hardened runtime, `MAP_JIT` may require the `com.apple.security.cs.allow-jit` entitlement.
- Windows/ARM32 is not a supported Go target.

## Project layout

```text
openscripting/
├── main/        Go lexer, parser, compiler, bytecode, VM, and native integrations
├── lib/         Bundled OpenScript modules (loaded from the executable's lib/ directory)
├── tests/       Regression scripts and module-loading fixtures
├── docs/        Language and API reference (docs/index.html)
├── build.sh     macOS / Linux build and cross-platform archive script
└── build.bat    Windows build and cross-platform archive script
```

## Contributing

Contributions are welcome, whether that's a bug report, a new test, a docs fix, or a library module.

1. Fork the repo and create a branch.
2. Keep **language behavior, regression scripts, and the reference aligned** when changing syntax or runtime APIs.
3. Build with the supported Go version and run the relevant scripts from `tests/`.
4. Open a pull request describing what changed and why.

Not sure where to start? Add a regression script to `tests/`, or write an example for the docs.

## Roadmap

<!-- Fill this in so visitors can see where the project is heading. Examples: -->

- [ ] Online playground
- [ ] Editor support (VS Code extension / Tree-sitter grammar)
- [ ] `finally` blocks
- [ ] More bundled libraries

## License

OpenScript is distributed under the [MIT License](LICENSE).

---

<div align="center">

If you find OpenScript interesting, consider giving it a ⭐ on [GitHub](https://github.com/HolliShake/openscripting).

</div>
