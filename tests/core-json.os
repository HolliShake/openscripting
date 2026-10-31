import { println } from "core:io";
import { parse, stringify } from "core:json";

const input = "{\"name\":\"Ada\",\"active\":true,\"score\":42,\"items\":[null,\"x\",2.5]}";
const value = parse(input);
const encoded = stringify(value);
const roundTrip = parse(encoded);
const cyclic = { self: null, kept: "value" };
cyclic->self = cyclic;

if (roundTrip->name != "Ada" || roundTrip->active != true || roundTrip->score != 42) {
    println("FAIL: JSON object round trip");
}
if (roundTrip->items[0] != null || roundTrip->items[1] != "x" || roundTrip->items[2] != 2.5) {
    println("FAIL: JSON array round trip");
}
if (stringify(["quote: \\\"", 1, false]) != "[\"quote: \\\\\\\"\",1,false]") {
    println("FAIL: JSON escaping");
}
if (stringify(cyclic) != "{\"kept\":\"value\"}") {
    println("FAIL: cyclic object property was not skipped");
}

try {
    parse("{invalid}");
    println("FAIL: invalid JSON was not raised");
} catch (err) {
    println("caught parse error:", err);
}

try {
    stringify(fn() {});
    println("FAIL: unsupported value was not raised");
} catch (err) {
    println("caught stringify error:", err);
}

println("core:json ok");
