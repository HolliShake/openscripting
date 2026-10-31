import { println } from "core:io";

var count = 0;
loop (;;) {
	count++;
	if (count == 3) {
		break;
	}
}

println(count);