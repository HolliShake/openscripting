import { println } from "core:io";
import { isEven: even, addOffset } from "./import.os";
import { nextCall: next } from "./import.os";

var offset = 100;
println(even(4));
println(even(5));
println(addOffset(5));
println(offset);
println(next());
println(next());
