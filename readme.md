# OpenScript

```text
 ██████╗ ██████╗ ███████╗███╗   ██╗███████╗ ██████╗██████╗ ██╗██████╗ ████████╗
██╔═══██╗██╔══██╗██╔════╝████╗  ██║██╔════╝██╔════╝██╔══██╗██║██╔══██╗╚══██╔══╝
██║   ██║██████╔╝█████╗  ██╔██╗ ██║███████╗██║     ██████╔╝██║██████╔╝   ██║
██║   ██║██╔═══╝ ██╔══╝  ██║╚██╗██║╚════██║██║     ██╔══██╗██║██╔═══╝    ██║
╚██████╔╝██║     ███████╗██║ ╚████║███████║╚██████╗██║  ██║██║██║        ██║
 ╚═════╝ ╚═╝     ╚══════╝╚═╝  ╚═══╝╚══════╝ ╚═════╝╚═╝  ╚═╝╚═╝╚═╝        ╚═╝
```

OpenScript is a small compiled scripting language implemented in Go. Its compiler emits bytecode that runs on a coroutine-based virtual machine. The CLI prints the colored version of the banner above with `-help` or when no script path is provided.

## Requirements

- Go 1.25.5 or newer.

## Build and run

From the repository root:

```powershell
go build -o openscript.exe ./main
```

Run a script with either supported flag:

```powershell
./openscript.exe -f .\tests\test.os
./openscript.exe -file .\tests\test.os
```

On Windows, `build.bat` builds `openscript.exe` and writes compiler escape-analysis findings to `build.txt`.

The language reference is available at [`docs/index.html`](docs/index.html).

## Language overview

Scripts use the `.os` extension. Import builtins by named exports:

```javascript
import { println } from "core:io";

const greeting = "Hello, OpenScript!";
println(greeting);
```

The language currently supports:

- `var`, `let`, and `const` declarations, including array and object destructuring.
- Numbers, strings, booleans, `null`, arrays, objects, functions, classes, and promises.
- Unicode identifiers and Unicode-aware string literals and lexer positions.
- Function expressions, named `name::fn(...)` declarations, closures, and variadic parameters (`values...`).
- Async functions and `await`, Promise `->then(...)` and `->catch(...)` chaining, and a microtask queue.
- Arrays and objects with spread, property/index access, and `map`, `filter`, and `find` callback methods.
- Classes, constructors, inheritance, `this`, and `super(...)`.
- `if`/`else`, `loop`, `switch` statements and expressions, `break`, and `continue`.
- `try`/`catch` for runtime errors. Uncaught synchronous errors and unhandled Promise rejections print script stack traces and exit unsuccessfully.
- Relative imports, `lib:` imports from the executable's `lib` directory, and named builtin imports.

Example function and control flow:

```javascript
var sum::fn(first, second, third) {
	return first + second + third;
};

try {
	println(sum(2, 3, 5));
} catch (err) {
	println(err);
}
```

## Built-in modules

Builtin modules require named imports and reject unknown export names.

| Module         | Purpose and exports                                                                                                                                                                                                                     |
| -------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `core:io`      | `print`, `println`, and `scan` for standard output and line input.                                                                                                                                                                      |
| `core:file`    | `readText`, `writeText`, `appendText`, `exists`, and `remove`.                                                                                                                                                                          |
| `core:math`    | `PI`, `E`, common rounding, trigonometric, exponential and logarithmic functions, `pow`, `min`, and `max`. Trigonometric functions use radians.                                                                                         |
| `core:number`  | `parseInt`, `parseFloat`, `isFinite`, `isNaN`, `sign`, `clamp`, `MAX_VALUE`, `MIN_VALUE`, `NaN`, and `Infinity`. Invalid numeric text parses to `null`.                                                                                 |
| `core:unicode` | Code-point and byte lengths, code-point conversion/indexing, validity checking, case conversion, and Unicode character predicates.                                                                                                      |
| `core:sys`     | Runtime OS, architecture, bitness, CPU count, process details, path separators, OS predicates, `argv` (script arguments after the script path), current directory, executable, hostname, home/temp directories, and environment lookup. |
| `core:path`    | Host path operations: existence/type checks, absolute paths, joining, cleaning, path parts, and relative paths.                                                                                                                         |
| `core:json`    | `parse` and `stringify` for JSON conversion.                                                                                                                                                                                            |
| `core:error`   | `Error(message, line?)` constructor with `message` and `line` properties.                                                                                                                                                               |
| `core:http`    | `Express` HTTP server and asynchronous `fetch`; see the reference for routes, request/response fields, limits, and options.                                                                                                             |

Example async operation:

```javascript
import { fetch } from "core:http";
import { println } from "core:io";

var check::fn() async {
	var response = await fetch("https://example.com/");
	println(response->status, response->ok);
	println(await response->text());
};

check()->catch(fn(err) {
	println("request failed", err);
});
```

HTTP error status codes resolve normally; network errors reject. Async functions return promises, and errors can be handled with `await`, `->catch(...)`, or a surrounding `try`/`catch` where applicable.

## Project layout

- `main/` contains the Go compiler, parser, bytecode runtime, and builtin modules.
- `lib/` contains importable OpenScript library modules.
- `tests/` contains `.os` examples and runtime regression scripts.
- `docs/index.html` is the detailed language and builtin reference.
