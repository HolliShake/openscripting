import { ArgumentParser } from "lib:argparse";
import { println } from "core:io";

var parser = ArgumentParser({prog: "demo", description: "example"});
parser->add_argument({dest: "file", help: "input file"});
parser->add_argument({flags: ["-v", "--verbose"], dest: "verbose", action: "count", help: "increase verbosity"});
parser->add_argument({flags: ["--number"], dest: "number", type: "int", "default": 7});
parser->add_argument({flags: ["--tag"], dest: "tag", action: "append", "default": []});
parser->add_argument({dest: "rest", nargs: "*"});
println("registered", parser->options->length, parser->positionals->length);

var parsed = parser->parse_args(["input.txt", "-vv", "--number=42", "--tag", "a", "--tag", "b", "x", "y"]);
println("parsed", parsed, parsed->file, parsed->verbose, parsed->number);
if (parsed->file != "input.txt" || parsed->verbose != 2 || parsed->number != 42 ||
	parsed->tag[0] != "a" || parsed->tag[1] != "b" || parsed->rest[0] != "x" || parsed->rest[1] != "y") {
	println("FAIL: argparse values", parsed);
}

var known = parser->parse_known_args(["input.txt", "--mystery", "--another"]);
if (known->unknown[0] != "--mystery" || known->unknown[1] != "--another") {
	println("FAIL: argparse unknown args", known);
}

var help = parser->parse_args(["--help"]);
if (help->help == null) {
	println("FAIL: argparse help", help);
}

var missing = parser->parse_args([]);
if (missing->error == null) {
	println("FAIL: missing required argument was not reported", missing);
}

println("argparse ok");
