import { println } from "core:io";
import { instance:first } from "./shared.os";
import { instance:second } from "./shared.os";

first->count = 9;
if (second->count != 9) {
    println("FAIL: module import did not share the exported instance");
}
println("module cache ok");
