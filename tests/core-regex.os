import { println } from "core:io";
import { Regex, compile, compilePOSIX, match, matchReader, matchString, mustCompile, mustCompilePOSIX, quoteMeta } from "core:regex";

const expression = Regex("(?P<word>[a-z]+)-(\\d+)");
const captures = expression->findStringSubmatch("item-42 next-7");
const indices = expression->findStringSubmatchIndex("item-42");
const all = expression->findAllString("item-42 next-7", -1);
const allSubmatches = expression->findAllStringSubmatch("item-42 next-7");
const names = expression->subexpNames();
const expanded = expression->expandString("${word}:$2", "item-42", indices);
const prefix = expression->literalPrefix();
const byteMatches = expression->findAll("item-42 next-7", -1);
const byteSubmatches = expression->findSubmatch("item-42");
const readerIndices = expression->findReaderSubmatchIndex("item-42");
const callbackReplacement = expression->replaceAllStringFunc("item-42", fn(value) { return value + "!"; });
const text = expression->marshalText();
const regexToClone = Regex("a|ab");
const longestClone = regexToClone->copy();
longestClone->longest();
const alreadyLongestClone = Regex("a|ab")->longest()->copy();
const posixClone = compilePOSIX("[[:digit:]]+")->copy();
if (expression->string() != "(?P<word>[a-z]+)-(\\d+)" || expression->numSubexp() != 2) {
    println("FAIL: regex metadata");
}
if (expression->matchString("item-42") != true || expression->matchString("42-item") != false) {
    println("FAIL: regex matching");
}
if (captures[0] != "item-42" || captures[1] != "item" || captures[2] != "42" || indices[0] != 0 || indices[1] != 7) {
    println("FAIL: regex capture values and indices");
}
if (all[0] != "item-42" || all[1] != "next-7" || allSubmatches[1][1] != "next") {
    println("FAIL: regex all matches");
}
if (byteMatches[0] != "item-42" || byteSubmatches[1] != "item" || readerIndices[0] != 0 || readerIndices[1] != 7) {
    println("FAIL: regex byte and reader adapters");
}
if (names[1] != "word" || names[2] != "" || expanded != "item:42") {
    println("FAIL: regex names and expansion");
}
if (expression->subexpIndex("word") != 1 || expression->subexpIndex("missing") != -1 || prefix[0] != "" || prefix[1] != false) {
    println("FAIL: regex subexpression metadata");
}
if (expression->replaceAllString("item-42", "${word}:$2") != "item:42" || expression->replaceAllLiteralString("item-42", "$1") != "$1") {
    println("FAIL: regex replacement");
}
if (callbackReplacement != "item-42!" || expression->replaceAllFunc("item-42", fn(value) { return value + "?"; }) != "item-42?") {
    println("FAIL: regex callback replacements");
}
const parts = Regex(",")->split("a,b,,c", -1);
if (parts[0] != "a" || parts[1] != "b" || parts[2] != "" || parts[3] != "c") {
    println("FAIL: regex split");
}
if (quoteMeta("a+b") != "a\\+b" || expression->findStringIndex("missing") != null) {
    println("FAIL: quoteMeta or missing match");
}
if (regexToClone->findString("ab") != "a" || longestClone->findString("ab") != "ab" || alreadyLongestClone->findString("ab") != "ab" || posixClone->matchString("42") != true) {
    println("FAIL: regex copy settings");
}
if (matchString("[0-9]+", "item-42") != true || match("[0-9]+", "item-42") != true || matchReader("[0-9]+", "item-42") != true) {
    println("FAIL: package regex match helpers");
}
if (compile("digits")->matchString("digits") != true || mustCompile("digits")->matchString("digits") != true || compilePOSIX("[[:digit:]]+")->matchString("42") != true || mustCompilePOSIX("[[:digit:]]+")->matchString("42") != true || Regex->compilePOSIX("[[:digit:]]+")->matchString("42") != true) {
    println("FAIL: regex package helpers or POSIX compilation");
}
const restored = Regex("unused");
if (restored->unmarshalText(text)->string() != expression->string() || restored->appendText("prefix:") != "prefix:" + expression->string()) {
    println("FAIL: regex text marshaling");
}

try {
    Regex("[");
    println("FAIL: invalid pattern was not raised");
} catch (err) {
    println("caught regex compile error:", err);
}

println("core:regex ok");