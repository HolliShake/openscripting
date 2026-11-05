/**
 * CryptoJS-compatible native-backed digest and encoding facade.
 * Legacy algorithms such as MD5, SHA-1, and RIPEMD160 are for interoperability,
 * not new security designs.
 */

import { CryptoJS: __CryptoJS } from "core:crypto";

const CryptoJS = __CryptoJS;
const WordArray = CryptoJS->lib->WordArray;
const enc = CryptoJS->enc;
const algo = CryptoJS->algo;
const MD5 = CryptoJS->MD5;
const SHA1 = CryptoJS->SHA1;
const SHA224 = CryptoJS->SHA224;
const SHA256 = CryptoJS->SHA256;
const SHA384 = CryptoJS->SHA384;
const SHA512 = CryptoJS->SHA512;
const SHA3 = CryptoJS->SHA3;
const RIPEMD160 = CryptoJS->RIPEMD160;
const HmacMD5 = CryptoJS->HmacMD5;
const HmacSHA1 = CryptoJS->HmacSHA1;
const HmacSHA224 = CryptoJS->HmacSHA224;
const HmacSHA256 = CryptoJS->HmacSHA256;
const HmacSHA384 = CryptoJS->HmacSHA384;
const HmacSHA512 = CryptoJS->HmacSHA512;
const HmacSHA3 = CryptoJS->HmacSHA3;
const HmacRIPEMD160 = CryptoJS->HmacRIPEMD160;
const PBKDF2 = CryptoJS->PBKDF2;
const EvpKDF = CryptoJS->EvpKDF;