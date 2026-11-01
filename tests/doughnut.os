import { cos: mathCos, floor: mathFloor, sin: mathSin } from "core:math";
import { print } from "core:io";

const width = 80;
const height = 24;
const size = width * height;

const z0 = [0];
const z1 = [...z0, ...z0];
const z2 = [...z1, ...z1];
const z3 = [...z2, ...z2];
const z4 = [...z3, ...z3];
const z5 = [...z4, ...z4];
const z6 = [...z5, ...z5];
const z7 = [...z6, ...z6];
const z8 = [...z7, ...z7];
const z9 = [...z8, ...z8];
const z10 = [...z9, ...z9];
const z11 = [...z10, ...z10];

const p0 = [" "];
const p1 = [...p0, ...p0];
const p2 = [...p1, ...p1];
const p3 = [...p2, ...p2];
const p4 = [...p3, ...p3];
const p5 = [...p4, ...p4];
const p6 = [...p5, ...p5];
const p7 = [...p6, ...p6];
const p8 = [...p7, ...p7];
const p9 = [...p8, ...p8];
const p10 = [...p9, ...p9];
const p11 = [...p10, ...p10];

const shades = [".", ",", "-", "~", ":", ";", "=", "!", "*", "#", "$", "@"]; 
var zBuffer = [...z11];
var pixels = [...p11];
var angleA = 0;
var angleB = 0;

print("\x1b[2J\x1b[H");

loop (;;) {
    loop (let clearIndex = 0; clearIndex < size; clearIndex++) {
        zBuffer[clearIndex] = 0;
        pixels[clearIndex] = " ";
    }

    let sinA = mathSin(angleA);
    let cosA = mathCos(angleA);
    let sinB = mathSin(angleB);
    let cosB = mathCos(angleB);

    loop (let theta = 0; theta < 6.28; theta += 0.07) {
        let sinTheta = mathSin(theta);
        let cosTheta = mathCos(theta);

        loop (let phi = 0; phi < 6.28; phi += 0.02) {
            let sinPhi = mathSin(phi);
            let cosPhi = mathCos(phi);
            let ring = cosPhi + 2;
            let depth = 1 / (sinTheta * ring * sinA + sinPhi * cosA + 5);
            let rotated = sinTheta * ring * cosA - sinPhi * sinA;
            let screenX = mathFloor(width / 2 + 30 * depth * (cosTheta * ring * cosB - rotated * sinB));
            let screenY = mathFloor(height / 2 + 15 * depth * (cosTheta * ring * sinB + rotated * cosB));
            let luminance = mathFloor(8 * ((sinPhi * sinA - sinTheta * cosPhi * cosA) * cosB - sinTheta * cosPhi * sinA - sinPhi * cosA - cosTheta * cosPhi * sinB));

            if (screenX >= 0 && screenX < width && screenY >= 0 && screenY < height && luminance > 0) {
                let index = screenX + width * screenY;
                if (depth > zBuffer[index]) {
                    if (luminance > 11) {
                        luminance = 11;
                    }
                    zBuffer[index] = depth;
                    pixels[index] = shades[luminance];
                }
            }
        }
    }

    let frame = "";
    loop (let row = 0; row < height; row++) {
        loop (let column = 0; column < width; column++) {
            frame = frame + pixels[row * width + column];
        }
        frame = frame + "\n";
    }
    print("\x1b[H" + frame);

    angleA += 0.04;
    angleB += 0.02;
}
