import { MD5, SHA1, SHA224, SHA256, SHA384, SHA512, SHA3, RIPEMD160, HmacSHA256 } from "lib:crypto";
import { CryptoJS: CryptoFacade } from "lib:crypto";
import { println } from "core:io";

var failures = 0;
if (MD5("abc")->toString() != "900150983cd24fb0d6963f7d28e17f72") {
	println("FAIL: MD5 abc");
	failures++;
}
if (SHA1("abc")->toString() != "a9993e364706816aba3e25717850c26c9cd0d89d") {
	println("FAIL: SHA-1 abc");
	failures++;
}
if (SHA224("abc")->toString() != "23097d223405d8228642a477bda255b32aadbce4bda0b3f7e36c9da7") {
	println("FAIL: SHA-224 abc");
	failures++;
}
if (SHA256("")->toString() != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855") {
	println("FAIL: SHA-256 empty string");
	failures++;
}
if (SHA384("abc")->toString() != "cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed8086072ba1e7cc2358baeca134c825a7") {
	println("FAIL: SHA-384 abc");
	failures++;
}
if (SHA512("abc")->toString() != "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f") {
	println("FAIL: SHA-512 abc");
	failures++;
}
if (SHA3("", { outputLength: 256 })->toString() != "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470") {
	println("FAIL: CryptoJS Keccak-256 empty string");
	failures++;
}
if (RIPEMD160("abc")->toString() != "8eb208f7e05d987a9b044a8e98c6b087f15a0bfc") {
	println("FAIL: RIPEMD-160 abc");
	failures++;
}
if (SHA256("abc")->toString() != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad") {
	println("FAIL: SHA-256 abc");
	failures++;
}
if (SHA256("é")->toString() != "4a99557e4033c3539de2eb65472017cad5f9557f7a0625a09f1c3f6e2ba69c4c") {
	println("FAIL: SHA-256 UTF-8");
	failures++;
}
if (HmacSHA256("The quick brown fox jumps over the lazy dog", "key")->toString() != "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8") {
	println("FAIL: HMAC-SHA-256 known vector");
	failures++;
}
if (CryptoFacade->algo->SHA256->create()->update("a")->update("bc")->finalize()->toString() != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad") {
	println("FAIL: incremental SHA-256");
	failures++;
}
if (CryptoFacade->enc->Hex->stringify(CryptoFacade->enc->Utf8->parse("abc")) != "616263") {
	println("FAIL: encoder round-trip");
	failures++;
}
if (CryptoFacade->lib->WordArray->create([1], 4)->toString() != "00000001") {
	println("FAIL: WordArray.create words and sigBytes");
	failures++;
}
var mutableWords = CryptoFacade->lib->WordArray->create([1], 4);
mutableWords->words[0] = 2;
if (mutableWords->toString() != "00000002") {
	println("FAIL: WordArray public words mutation");
	failures++;
}
if (CryptoFacade->PBKDF2("password", "salt", { keySize: 5, iterations: 1 })->toString() != "0c60c80f961f0e71f3a9b524af6012062fe037a6") {
	println("FAIL: PBKDF2-SHA1 vector");
	failures++;
}
if (CryptoFacade->SHA3("", { outputLength: 256 })->toString() != "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470") {
	println("FAIL: SHA3 default compatibility");
	failures++;
}

if (failures == 0) {
	println("native-backed crypto vectors passed");
} else {
	println("native-backed crypto vectors failed");
}
