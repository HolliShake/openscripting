import { print } from "core:io";
import { abs, cos, floor, max, min, sin } from "core:math";

const width = 64;
const height = 23;
const cameraDistance = 4.5;
const frameDelayIterations = 250000;
const blank4 = [" ", " ", " ", " "];
const blank8 = [...blank4, ...blank4];
const blank16 = [...blank8, ...blank8];
const blank32 = [...blank16, ...blank16];
const blankRow = [...blank32, ...blank32];
const emptyGrid = [null, null, null, null, null, null, null, null, null, null, null, null, null, null, null, null, null, null, null, null, null, null, null];
const depth0 = [0];
const depth1 = [...depth0, ...depth0];
const depth2 = [...depth1, ...depth1];
const depth3 = [...depth2, ...depth2];
const depth4 = [...depth3, ...depth3];
const depth5 = [...depth4, ...depth4];
const depth6 = [...depth5, ...depth5];
const depth7 = [...depth6, ...depth6];
const depth8 = [...depth7, ...depth7];
const depth9 = [...depth8, ...depth8];
const depth10 = [...depth9, ...depth9];
const depthBufferTemplate = [...depth10, ...depth10];
const faces = [
	[0, 0, 1, 1, 0, 0, 0, 1, 0, "38;5;196", "38;5;124"],
	[0, 0, -1, -1, 0, 0, 0, 1, 0, "38;5;208", "38;5;130"],
	[0, 1, 0, 1, 0, 0, 0, 0, -1, "38;5;15", "38;5;245"],
	[0, -1, 0, 1, 0, 0, 0, 0, 1, "38;5;226", "38;5;136"],
	[1, 0, 0, 0, 0, -1, 0, 1, 0, "38;5;21", "38;5;18"],
	[-1, 0, 0, 0, 0, 1, 0, 1, 0, "38;5;46", "38;5;22"]
];

var rotatePoint::fn(point, angleX, angleY, angleZ) {
	let x = point[0];
	let y = point[1];
	let z = point[2];

	let rotatedY = y * cos(angleX) - z * sin(angleX);
	let rotatedZ = y * sin(angleX) + z * cos(angleX);
	y = rotatedY;
	z = rotatedZ;

	let rotatedX = x * cos(angleY) + z * sin(angleY);
	rotatedZ = -x * sin(angleY) + z * cos(angleY);
	x = rotatedX;
	z = rotatedZ;

	rotatedX = x * cos(angleZ) - y * sin(angleZ);
	rotatedY = x * sin(angleZ) + y * cos(angleZ);
	x = rotatedX;
	y = rotatedY;

	let perspective = cameraDistance / (cameraDistance + z);
	return [width / 2 + x * perspective * 15, height / 2 - y * perspective * 7, 1 / (cameraDistance + z)];
};

var rotateVector::fn(vector, angleX, angleY, angleZ) {
	let x = vector[0];
	let y = vector[1];
	let z = vector[2];
	let rotatedY = y * cos(angleX) - z * sin(angleX);
	let rotatedZ = y * sin(angleX) + z * cos(angleX);
	y = rotatedY;
	z = rotatedZ;
	let rotatedX = x * cos(angleY) + z * sin(angleY);
	rotatedZ = -x * sin(angleY) + z * cos(angleY);
	x = rotatedX;
	z = rotatedZ;
	rotatedX = x * cos(angleZ) - y * sin(angleZ);
	rotatedY = x * sin(angleZ) + y * cos(angleZ);
	return [rotatedX, rotatedY, z];
};

var facePoint::fn(face, horizontal, vertical) {
	return [
		face[0] + face[3] * horizontal + face[6] * vertical,
		face[1] + face[4] * horizontal + face[7] * vertical,
		face[2] + face[5] * horizontal + face[8] * vertical
	];
};

var drawTriangle::fn(grid, depthBuffer, first, second, third, color) {
	let denominator = (second[1] - third[1]) * (first[0] - third[0]) + (third[0] - second[0]) * (first[1] - third[1]);
	if (abs(denominator) < 0.000001) {
		return null;
	}

	let minX = floor(min(first[0], min(second[0], third[0])));
	let maxX = floor(max(first[0], max(second[0], third[0])));
	let minY = floor(min(first[1], min(second[1], third[1])));
	let maxY = floor(max(first[1], max(second[1], third[1])));
	if (minX < 0) { minX = 0; }
	if (maxX >= width) { maxX = width - 1; }
	if (minY < 0) { minY = 0; }
	if (maxY >= height) { maxY = height - 1; }

	loop (let y = minY; y <= maxY; y++) {
		loop (let x = minX; x <= maxX; x++) {
			let weight0 = ((second[1] - third[1]) * (x - third[0]) + (third[0] - second[0]) * (y - third[1])) / denominator;
			let weight1 = ((third[1] - first[1]) * (x - third[0]) + (first[0] - third[0]) * (y - third[1])) / denominator;
			let weight2 = 1 - weight0 - weight1;
			if (weight0 >= 0 && weight1 >= 0 && weight2 >= 0) {
				let depth = weight0 * first[2] + weight1 * second[2] + weight2 * third[2];
				let index = y * width + x;
				if (depth > depthBuffer[index]) {
					depthBuffer[index] = depth;
					grid[y][x] = "\x1b[" + color + "m█\x1b[0m";
				}
			}
		}
	}
};

var angleX = 0;
var angleY = 0;
var angleZ = 0;
loop (;;) {
	let grid = [...emptyGrid];
	let depthBuffer = [...depthBufferTemplate];

	loop (let row = 0; row < height; row++) {
		grid[row] = [...blankRow];
	}
	loop (let faceIndex = 0; faceIndex < faces->len(); faceIndex++) {
		let face = faces[faceIndex];
		let normal = rotateVector([face[0], face[1], face[2]], angleX, angleY, angleZ);
		let brightness = 0.55 + max(0, -0.35 * normal[0] + 0.55 * normal[1] - 0.75 * normal[2]) * 0.45;
		let color = face[9];
		if (brightness < 0.75) {
			color = face[10];
		}

		loop (let stickerY = 0; stickerY < 3; stickerY++) {
			loop (let stickerX = 0; stickerX < 3; stickerX++) {
				let centerX = -0.6667 + stickerX * 0.6667;
				let centerY = 0.6667 - stickerY * 0.6667;
				let cornerA = rotatePoint(facePoint(face, centerX - 0.30, centerY - 0.30), angleX, angleY, angleZ);
				let cornerB = rotatePoint(facePoint(face, centerX + 0.30, centerY - 0.30), angleX, angleY, angleZ);
				let cornerC = rotatePoint(facePoint(face, centerX + 0.30, centerY + 0.30), angleX, angleY, angleZ);
				let cornerD = rotatePoint(facePoint(face, centerX - 0.30, centerY + 0.30), angleX, angleY, angleZ);
				drawTriangle(grid, depthBuffer, cornerA, cornerB, cornerC, color);
				drawTriangle(grid, depthBuffer, cornerA, cornerC, cornerD, color);
			}
		}
	}

	let output = "\x1b[H\x1b[2J\x1b[1;36mOPEN SCRIPT  /  RUBIK'S CUBE\x1b[0m\n\n";
	loop (let row = 0; row < height; row++) {
		loop (let column = 0; column < width; column++) {
			output += grid[row][column];
		}
		output += "\n";
	}
	output += "\n\x1b[2m3 x 3 facelets  |  three-axis rotation\x1b[0m";
	print(output);

	angleX += 0.018;
	angleY += 0.026;
	angleZ += 0.01;
	if (angleX >= 6.28) { angleX -= 6.28; }
	if (angleY >= 6.28) { angleY -= 6.28; }
	if (angleZ >= 6.28) { angleZ -= 6.28; }

	loop (let delay = 0; delay < frameDelayIterations; delay++) {
	}
}
