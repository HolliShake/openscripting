import { floor: mathFloor, logarithm: mathLog, sqrt: mathSqrt } from "core:math";
import { print } from "core:io";

const width = 80;
const height = 60;
const terminalHeight = 30;
const maxIterations = 120;
const minReal = -1.6;
const maxReal = 1.6;
const minImaginary = -1.2;
const maxImaginary = 1.2;
const constantReal = -0.8;
const constantImaginary = 0.156;
const realStep = (maxReal - minReal) / width;
const imaginaryStep = (maxImaginary - minImaginary) / height;
const colorCodes = [
	"17", "18", "19", "20", "21", "27", "33", "39",
	"45", "51", "50", "49", "48", "47", "46", "82",
	"118", "154", "190", "226", "220", "214", "208", "202",
	"198", "164", "129", "93", "57", "21", "27", "33"
];

var frame = "\x1b[2J\x1b[H";
loop (let terminalRow = 0; terminalRow < terminalHeight; terminalRow++) {
	let upperImaginary = maxImaginary - terminalRow * 2 * imaginaryStep;
	let lowerImaginary = upperImaginary - imaginaryStep;
	loop (let column = 0; column < width; column++) {
		let real = minReal + column * realStep;
		let upperRealPart = real;
		let upperImaginaryPart = upperImaginary;
		let upperIteration = 0;
		let upperMagnitude = 0;

		loop (upperIteration < maxIterations && upperRealPart * upperRealPart + upperImaginaryPart * upperImaginaryPart <= 4) {
			let nextReal = upperRealPart * upperRealPart - upperImaginaryPart * upperImaginaryPart + constantReal;
			upperImaginaryPart = 2 * upperRealPart * upperImaginaryPart + constantImaginary;
			upperRealPart = nextReal;
			upperIteration++;
		}
		upperMagnitude = upperRealPart * upperRealPart + upperImaginaryPart * upperImaginaryPart;
		let upperColor = "16";
		if (upperIteration < maxIterations) {
			let smooth = upperIteration + 1 - mathLog(mathLog(mathSqrt(upperMagnitude))) / mathLog(2);
			let colorIndex = mathFloor(smooth * 2);
			loop (colorIndex >= 32) {
				colorIndex -= 32;
			}
			upperColor = colorCodes[colorIndex];
		}

		let lowerRealPart = real;
		let lowerImaginaryPart = lowerImaginary;
		let lowerIteration = 0;
		let lowerMagnitude = 0;
		loop (lowerIteration < maxIterations && lowerRealPart * lowerRealPart + lowerImaginaryPart * lowerImaginaryPart <= 4) {
			let nextReal = lowerRealPart * lowerRealPart - lowerImaginaryPart * lowerImaginaryPart + constantReal;
			lowerImaginaryPart = 2 * lowerRealPart * lowerImaginaryPart + constantImaginary;
			lowerRealPart = nextReal;
			lowerIteration++;
		}
		lowerMagnitude = lowerRealPart * lowerRealPart + lowerImaginaryPart * lowerImaginaryPart;
		let lowerColor = "16";
		if (lowerIteration < maxIterations) {
			let smooth = lowerIteration + 1 - mathLog(mathLog(mathSqrt(lowerMagnitude))) / mathLog(2);
			let colorIndex = mathFloor(smooth * 2);
			loop (colorIndex >= 32) {
				colorIndex -= 32;
			}
			lowerColor = colorCodes[colorIndex];
		}

		frame = frame + "\x1b[38;5;" + upperColor + ";48;5;" + lowerColor + "m▀";
	}
	frame = frame + "\x1b[0m\n";
}

frame = frame + "\x1b[0m";
print(frame);
