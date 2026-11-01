import { floor: mathFloor } from "core:math";
import { print } from "core:io";

const width = 80;
const height = 30;
const maxIterations = 72;
const minReal = -2.2;
const maxReal = 1;
const minImaginary = -1.2;
const maxImaginary = 1.2;
const realStep = (maxReal - minReal) / width;
const imaginaryStep = (maxImaginary - minImaginary) / height;
const palette = [
	"\x1b[38;5;17m█", "\x1b[38;5;18m█", "\x1b[38;5;19m█", "\x1b[38;5;20m█",
	"\x1b[38;5;21m█", "\x1b[38;5;27m█", "\x1b[38;5;33m█", "\x1b[38;5;39m█",
	"\x1b[38;5;45m█", "\x1b[38;5;51m█", "\x1b[38;5;50m█", "\x1b[38;5;49m█",
	"\x1b[38;5;48m█", "\x1b[38;5;47m█", "\x1b[38;5;46m█", "\x1b[38;5;82m█",
	"\x1b[38;5;118m█", "\x1b[38;5;154m█", "\x1b[38;5;190m█", "\x1b[38;5;226m█",
	"\x1b[38;5;220m█", "\x1b[38;5;214m█", "\x1b[38;5;208m█", "\x1b[38;5;202m█",
	"\x1b[38;5;198m█", "\x1b[38;5;164m█", "\x1b[38;5;129m█", "\x1b[38;5;93m█",
	"\x1b[38;5;57m█", "\x1b[38;5;21m█", "\x1b[38;5;27m█", "\x1b[38;5;33m█"
];

var frame = "\x1b[2J\x1b[H";
loop (let row = 0; row < height; row++) {
	let imaginary = maxImaginary - row * imaginaryStep;
	loop (let column = 0; column < width; column++) {
		let real = minReal + column * realStep;
		let realPart = 0;
		let imaginaryPart = 0;
		let iteration = 0;

		loop (iteration < maxIterations && realPart * realPart + imaginaryPart * imaginaryPart <= 4) {
			let nextReal = realPart * realPart - imaginaryPart * imaginaryPart + real;
			imaginaryPart = 2 * realPart * imaginaryPart + imaginary;
			realPart = nextReal;
			iteration++;
		}

		if (iteration == maxIterations) {
			frame = frame + "\x1b[38;5;16m█";
		} else {
			let colorIndex = mathFloor(iteration * 32 / maxIterations);
			if (colorIndex > 31) {
				colorIndex = 31;
			}
			frame = frame + palette[colorIndex];
		}
	}
	frame = frame + "\x1b[0m\n";
}

frame = frame + "\x1b[0m";
print(frame);
