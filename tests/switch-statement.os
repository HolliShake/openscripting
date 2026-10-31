import { println } from "core:io";

switch (4) {
    case 0:
    case 1, 2, 3, 4: {

    }
    println("Hello");
    break;
    default:
        println("No val!");
}

switch (2) {
    case 1:
        println("wrong");
    default:
        println("default fallthrough");
    case 2:
        println("later match");
        break;
}

switch (9) {
    case 1:
        println("no match should skip this");
}

switch (1) {
    case 1:
        println("continue to next case");
        continue;
        println("skipped case tail");
    case 2:
        println("next case body");
        continue;
    case 3:
        println("final case body skipped");
}

var loopValue = 0;
loop (loopValue < 2) {
    switch (loopValue) {
        case 0:
            println("switch break");
            break;
    }
    println("loop continues");
    loopValue++;
}

println("after switch");