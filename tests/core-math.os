import { random } from "core:math";
import { println } from "core:io";

loop (let sample = 0; sample < 256; sample++) {
	let value = random();
	if (value < 0 || value >= 1) {
		println("FAIL: random result out of range", value);
	}
}

println("core math random ok");