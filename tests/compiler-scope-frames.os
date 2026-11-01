import { cos: mathCos, sin: mathSin } from "core:math";
import { println } from "core:io";

var loopIndex = 0;
loop (loopIndex < 3) {
	let sine = mathSin(loopIndex);
	let cosine = mathCos(loopIndex);
	{
		let local = sine + cosine;
		let printLocal = fn() {
			println(local);
		};
		printLocal();
	}
	loopIndex++;
}

try {
	let zero = 0;
	let failure = 1 / zero;
} catch (err) {
	println(err->message);
}