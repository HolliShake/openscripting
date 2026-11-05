import { println } from "core:io";
import { assertSameKeys, bold, printTable, confirm } from "lib:tui";

if (bold("ready") != "\x1b[1mready\x1b[0m") {
	println("FAIL: terminal styling");
}
if (!confirm("Continue?")) {
	println("FAIL: terminal confirmation");
}

const rows = [
	{"COLUMN 1": 1, "COLUMN 2": 2000000000}
];
if (!assertSameKeys(rows) || !assertSameKeys([{left: 1, right: 2}, {right: 3, left: 4}])) {
	println("FAIL: identical object keys assertion");
}
if (assertSameKeys([{left: 1}, {right: 2}]) || assertSameKeys([{left: 1}, 2])) {
	println("FAIL: mismatched object keys assertion");
}
printTable(rows);
printTable([]);

println("terminal UI library ok");