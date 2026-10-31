import { println } from "core:io";

var values = ["plain", "with \"quotes\"", "line\nbreak"];
var record = {
	name: "Ada",
	values: values
};

println("top-level string");
println(values);
println(record);