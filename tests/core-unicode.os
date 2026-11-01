import {
	byteLength,
	codePointAt,
	fromCodePoint,
	isDigit,
	isLetter,
	isLower,
	isNumber,
	isUpper,
	isWhitespace,
	length,
	toLower,
	toUpper
} from "core:unicode";
import { println } from "core:io";

const text = "Aé😀";
if (length(text) != 3 || byteLength(text) != 7) {
	println("FAIL: Unicode length or byte length");
}
if (codePointAt(text, 1) != 233 || codePointAt(text, -1) != 128512) {
	println("FAIL: codePointAt");
}
if (codePointAt(text, 3) != null || fromCodePoint(128512) != "😀") {
	println("FAIL: code-point bounds or conversion");
}
if (toUpper("café") != "CAFÉ" || toLower("ÄΩ") != "äω") {
	println("FAIL: Unicode case conversion");
}
if (!isLetter("界") || !isDigit("٣") || !isNumber("Ⅷ") || !isUpper("Ω") || !isLower("я") || !isWhitespace(" ")) {
	println("FAIL: Unicode predicates");
}

println("core unicode ok");