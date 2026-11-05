package main

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"hash"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"crypto/pbkdf2"

	"golang.org/x/crypto/ripemd160"
	xsha3 "golang.org/x/crypto/sha3"
)

type cryptoWordArray struct {
	bytes []byte
}

type cryptoHasher struct {
	state hash.Hash
}

var cryptoHasherClass *Class

type cryptoEncoder struct {
	name string
}

var cryptoWordArrayClass *Class
var cryptoEncoderClass *Class

func cryptoFunction(paramCount int, callable func(*VM, []OSValue) OSValue) OSValue {
	return OSValue{Type: OSVNativeFunction, Opaque: unsafe.Pointer(&NativeFunction{ParamCount: paramCount, Callable: callable})}
}

func cryptoString(value string) OSValue {
	return OSValue{Type: OSVString, Number: float64(len(value)), Opaque: unsafe.Pointer(unsafe.StringData(value))}
}

func cryptoBytesValue(data []byte) OSValue {
	copyData := append([]byte(nil), data...)
	words := make([]OSValue, (len(copyData)+3)/4)
	for index := range words {
		var word uint32
		for offset := 0; offset < 4; offset++ {
			byteIndex := index*4 + offset
			word <<= 8
			if byteIndex < len(copyData) {
				word |= uint32(copyData[byteIndex])
			}
		}
		words[index] = OSValue{Type: OSVNum, Number: float64(word)}
	}
	object := &Object{
		ClassName: "WordArray",
		Class:     ensureCryptoWordArrayClass(),
		Native:    &cryptoWordArray{bytes: copyData},
		Values: map[string]OSValue{
			"words":    {Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: words})},
			"sigBytes": {Type: OSVNum, Number: float64(len(copyData))},
		},
	}
	return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(object)}
}

func cryptoObject(values map[string]OSValue, native any) OSValue {
	return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(&Object{Values: values, Native: native})}
}

func cryptoWordArrayBytes(value OSValue) ([]byte, bool) {
	if value.Type != OSVObject || value.Opaque == nil {
		return nil, false
	}
	object := (*Object)(value.Opaque)
	state, ok := object.Native.(*cryptoWordArray)
	if !ok || state == nil {
		return nil, false
	}
	wordsValue, wordsExists := object.Values["words"]
	sigBytesValue, sigBytesExists := object.Values["sigBytes"]
	if !wordsExists || wordsValue.Type != OSVArray || wordsValue.Opaque == nil || !sigBytesExists || !IsNumber(sigBytesValue) {
		return nil, false
	}
	words := (*Array)(wordsValue.Opaque).Elements
	sigBytes := int(sigBytesValue.Number)
	if sigBytes < 0 || sigBytes > len(words)*4 {
		return nil, false
	}
	data := make([]byte, len(words)*4)
	for index, wordValue := range words {
		if !IsNumber(wordValue) {
			return nil, false
		}
		word := uint32(int64(wordValue.Number))
		data[index*4] = byte(word >> 24)
		data[index*4+1] = byte(word >> 16)
		data[index*4+2] = byte(word >> 8)
		data[index*4+3] = byte(word)
	}
	return data[:sigBytes], true
}

func ensureCryptoWordArrayClass() *Class {
	if cryptoWordArrayClass != nil {
		return cryptoWordArrayClass
	}
	cryptoWordArrayClass = &Class{Name: "WordArray", Template: map[string]OSValue{
		"toString": nativeMethodTemplate(-1, cryptoWordArrayToString),
		"concat":   nativeMethodTemplate(1, cryptoWordArrayConcat),
		"clamp":    nativeMethodTemplate(0, cryptoWordArrayClamp),
		"clone":    nativeMethodTemplate(0, cryptoWordArrayClone),
	}}
	return cryptoWordArrayClass
}

func cryptoWordArrayToString(this OSValue, vm *VM, args []OSValue) OSValue {
	if len(args) > 1 {
		vm.NativeError = "WordArray.toString accepts at most one encoder"
		return vm.Null
	}
	bytes, ok := cryptoWordArrayBytes(this)
	if !ok {
		vm.NativeError = "invalid WordArray receiver"
		return vm.Null
	}
	encoding := "hex"
	if len(args) == 1 {
		if args[0].Type != OSVObject || args[0].Opaque == nil {
			vm.NativeError = "WordArray.toString expects an encoder object"
			return vm.Null
		}
		encoder, ok := (*Object)(args[0].Opaque).Native.(*cryptoEncoder)
		if !ok {
			vm.NativeError = "WordArray.toString expects a supported encoder"
			return vm.Null
		}
		encoding = encoder.name
	}
	return cryptoString(cryptoEncode(encoding, bytes))
}

func cryptoWordArrayConcat(this OSValue, vm *VM, args []OSValue) OSValue {
	left, ok := cryptoWordArrayBytes(this)
	if !ok {
		vm.NativeError = "invalid WordArray receiver"
		return vm.Null
	}
	right, ok := cryptoWordArrayBytes(args[0])
	if !ok {
		vm.NativeError = "WordArray.concat expects a WordArray"
		return vm.Null
	}
	combined := append(append([]byte(nil), left...), right...)
	state := (*Object)(this.Opaque).Native.(*cryptoWordArray)
	state.bytes = combined
	(*Object)(this.Opaque).Values["sigBytes"] = OSValue{Type: OSVNum, Number: float64(len(combined))}
	(*Object)(this.Opaque).Values["words"] = cryptoWordsValue(combined)
	return this
}

func cryptoWordArrayClamp(this OSValue, vm *VM, _ []OSValue) OSValue {
	bytes, ok := cryptoWordArrayBytes(this)
	if !ok {
		vm.NativeError = "invalid WordArray receiver"
		return vm.Null
	}
	(*Object)(this.Opaque).Values["words"] = cryptoWordsValue(bytes)
	return this
}

func cryptoWordArrayClone(this OSValue, vm *VM, _ []OSValue) OSValue {
	bytes, ok := cryptoWordArrayBytes(this)
	if !ok {
		vm.NativeError = "invalid WordArray receiver"
		return vm.Null
	}
	return cryptoBytesValue(bytes)
}

func cryptoWordsValue(data []byte) OSValue {
	words := make([]OSValue, (len(data)+3)/4)
	for index := range words {
		var word uint32
		for offset := 0; offset < 4; offset++ {
			word <<= 8
			byteIndex := index*4 + offset
			if byteIndex < len(data) {
				word |= uint32(data[byteIndex])
			}
		}
		words[index] = OSValue{Type: OSVNum, Number: float64(word)}
	}
	return OSValue{Type: OSVArray, Opaque: unsafe.Pointer(&Array{Elements: words})}
}

func cryptoBytesFromValue(value OSValue) ([]byte, bool) {
	if bytes, ok := cryptoWordArrayBytes(value); ok {
		return bytes, true
	}
	if value.Type == OSVString {
		return []byte(value.StringValue()), true
	}
	return nil, false
}

func cryptoEncode(encoding string, data []byte) string {
	switch strings.ToLower(encoding) {
	case "hex":
		return hex.EncodeToString(data)
	case "base64":
		return base64.StdEncoding.EncodeToString(data)
	case "base64url":
		return base64.RawURLEncoding.EncodeToString(data)
	case "utf8":
		return string(data)
	case "latin1":
		runes := make([]rune, len(data))
		for index, value := range data {
			runes[index] = rune(value)
		}
		return string(runes)
	case "utf16be":
		return decodeUTF16(data, false)
	case "utf16le":
		return decodeUTF16(data, true)
	default:
		return hex.EncodeToString(data)
	}
}

func cryptoEncoderParse(this OSValue, vm *VM, args []OSValue) OSValue {
	if this.Type != OSVObject || this.Opaque == nil || len(args) != 1 {
		vm.NativeError = "encoder.parse expects one string"
		return vm.Null
	}
	encoder, ok := (*Object)(this.Opaque).Native.(*cryptoEncoder)
	if !ok {
		vm.NativeError = "invalid encoder receiver"
		return vm.Null
	}
	return cryptoParse(vm, encoder.name, args[0])
}

func cryptoEncoderStringify(this OSValue, vm *VM, args []OSValue) OSValue {
	if this.Type != OSVObject || this.Opaque == nil || len(args) != 1 {
		vm.NativeError = "encoder.stringify expects one WordArray"
		return vm.Null
	}
	encoder, ok := (*Object)(this.Opaque).Native.(*cryptoEncoder)
	if !ok {
		vm.NativeError = "invalid encoder receiver"
		return vm.Null
	}
	bytes, ok := cryptoWordArrayBytes(args[0])
	if !ok {
		vm.NativeError = "encoder.stringify expects a WordArray"
		return vm.Null
	}
	return cryptoString(cryptoEncode(encoder.name, bytes))
}

func cryptoHashFactory(name string, outputLength int) (func() hash.Hash, int, bool) {
	switch strings.ToUpper(name) {
	case "MD5":
		return md5.New, md5.Size, true
	case "SHA1":
		return sha1.New, sha1.Size, true
	case "SHA224":
		return sha256.New224, sha256.Size224, true
	case "SHA256":
		return sha256.New, sha256.Size, true
	case "SHA384":
		return sha512.New384, sha512.Size384, true
	case "SHA512":
		return sha512.New, sha512.Size, true
	case "SHA3":
		switch outputLength {
		case 256:
			return xsha3.NewLegacyKeccak256, 32, true
		case 0, 512:
			return xsha3.NewLegacyKeccak512, 64, true
		default:
			return nil, 0, false
		}
	case "RIPEMD160":
		return ripemd160.New, ripemd160.Size, true
	default:
		return nil, 0, false
	}
}

func cryptoText(value OSValue) (string, bool) {
	if value.Type != OSVString {
		return "", false
	}
	return value.StringValue(), true
}

func cryptoConfigNumber(config OSValue, property string, fallback int) int {
	if config.Type != OSVObject || config.Opaque == nil {
		return fallback
	}
	value, exists := (*Object)(config.Opaque).Values[property]
	if !exists || !IsNumber(value) {
		return fallback
	}
	return int(value.Number)
}

func nativeCryptoDigest(vm *VM, args []OSValue) OSValue {
	if len(args) < 2 || len(args) > 3 {
		vm.NativeError = "core:crypto.digest expects algorithm, message, and optional config"
		return vm.Null
	}
	name, _ := cryptoText(args[0])
	message, ok := cryptoText(args[1])
	if !ok {
		vm.NativeError = "core:crypto.digest expects an algorithm and string message"
		return vm.Null
	}
	outputLength := 0
	if len(args) > 2 {
		outputLength = cryptoConfigNumber(args[2], "outputLength", 0)
	}
	factory, _, exists := cryptoHashFactory(name, outputLength)
	if !exists {
		vm.NativeError = "core:crypto.digest unsupported algorithm or outputLength"
		return vm.Null
	}
	state := factory()
	_, _ = state.Write([]byte(message))
	return cryptoBytesValue(state.Sum(nil))
}

func nativeCryptoHasherCreate(config cryptoHasherConfig) func(*VM, []OSValue) OSValue {
	return func(vm *VM, args []OSValue) OSValue {
		if len(args) > 0 {
			config.outputLength = cryptoConfigNumber(args[0], "outputLength", config.outputLength)
		}
		factory, _, exists := cryptoHashFactory(config.name, config.outputLength)
		if !exists {
			vm.NativeError = "core:crypto hasher unsupported algorithm or outputLength"
			return vm.Null
		}
		if cryptoHasherClass == nil {
			cryptoHasherClass = &Class{Name: "Hasher", Template: map[string]OSValue{
				"update":   nativeMethodTemplate(1, cryptoHasherUpdate),
				"finalize": nativeMethodTemplate(-1, cryptoHasherFinalize),
			}}
		}
		return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(&Object{
			ClassName: "Hasher", Class: cryptoHasherClass,
			Native: &cryptoHasher{state: factory()}, Values: map[string]OSValue{},
		})}
	}
}

type cryptoHasherConfig struct {
	name         string
	outputLength int
}

func cryptoHasherUpdate(this OSValue, vm *VM, args []OSValue) OSValue {
	if this.Type != OSVObject || this.Opaque == nil {
		vm.NativeError = "invalid hasher receiver"
		return vm.Null
	}
	hasher, ok := (*Object)(this.Opaque).Native.(*cryptoHasher)
	data, dataOK := cryptoBytesFromValue(args[0])
	if !ok || !dataOK {
		vm.NativeError = "Hasher.update expects a string or WordArray"
		return vm.Null
	}
	_, _ = hasher.state.Write(data)
	return this
}

func cryptoHasherFinalize(this OSValue, vm *VM, args []OSValue) OSValue {
	if this.Type != OSVObject || this.Opaque == nil {
		vm.NativeError = "invalid hasher receiver"
		return vm.Null
	}
	hasher, ok := (*Object)(this.Opaque).Native.(*cryptoHasher)
	if !ok {
		vm.NativeError = "invalid hasher receiver"
		return vm.Null
	}
	if len(args) > 0 && args[0].Type != OSVNull {
		data, dataOK := cryptoBytesFromValue(args[0])
		if !dataOK {
			vm.NativeError = "Hasher.finalize expects a string or WordArray"
			return vm.Null
		}
		_, _ = hasher.state.Write(data)
	}
	return cryptoBytesValue(hasher.state.Sum(nil))
}

func nativeCryptoHMAC(vm *VM, args []OSValue) OSValue {
	if len(args) < 3 || len(args) > 4 {
		vm.NativeError = "core:crypto.hmac expects algorithm, message, key, and optional config"
		return vm.Null
	}
	name, _ := cryptoText(args[0])
	message, messageOK := cryptoText(args[1])
	key, keyOK := cryptoText(args[2])
	if !messageOK || !keyOK {
		vm.NativeError = "core:crypto.hmac expects an algorithm, message, and key strings"
		return vm.Null
	}
	outputLength := 0
	if len(args) > 3 {
		outputLength = cryptoConfigNumber(args[3], "outputLength", 0)
	}
	factory, _, exists := cryptoHashFactory(name, outputLength)
	if !exists {
		vm.NativeError = "core:crypto.hmac unsupported algorithm or outputLength"
		return vm.Null
	}
	state := hmac.New(factory, []byte(key))
	_, _ = state.Write([]byte(message))
	return cryptoBytesValue(state.Sum(nil))
}

func nativeCryptoPBKDF2(vm *VM, args []OSValue) OSValue {
	if len(args) != 5 {
		vm.NativeError = "core:crypto.pbkdf2 expects password, salt, iterations, key length, and hasher"
		return vm.Null
	}
	password, passwordOK := cryptoText(args[0])
	salt, saltOK := cryptoText(args[1])
	if !passwordOK || !saltOK || !IsNumber(args[2]) || !IsNumber(args[3]) {
		vm.NativeError = "core:crypto.pbkdf2 expects password, salt, iterations, and key length"
		return vm.Null
	}
	name, _ := cryptoText(args[4])
	factory, _, exists := cryptoHashFactory(name, 0)
	iterations, keyLength := int(args[2].Number), int(args[3].Number)
	if !exists || iterations < 1 || iterations > 10000000 || keyLength < 1 || keyLength > 1048576 {
		vm.NativeError = "core:crypto.pbkdf2 invalid hasher, iteration count, or key length"
		return vm.Null
	}
	derived, err := pbkdf2.Key(factory, password, []byte(salt), iterations, keyLength)
	if err != nil {
		vm.NativeError = "core:crypto.pbkdf2: " + err.Error()
		return vm.Null
	}
	return cryptoBytesValue(derived)
}

func nativeCryptoEvpKDF(vm *VM, args []OSValue) OSValue {
	if len(args) != 5 {
		vm.NativeError = "core:crypto.evpKDF expects password, salt, iterations, key length, and hasher"
		return vm.Null
	}
	password, passwordOK := cryptoText(args[0])
	salt, saltOK := cryptoText(args[1])
	if !passwordOK || !saltOK || !IsNumber(args[2]) || !IsNumber(args[3]) {
		vm.NativeError = "core:crypto.evpKDF expects password, salt, iterations, and key length"
		return vm.Null
	}
	name, _ := cryptoText(args[4])
	factory, _, exists := cryptoHashFactory(name, 0)
	iterations, keyLength := int(args[2].Number), int(args[3].Number)
	if !exists || iterations < 1 || iterations > 10000000 || keyLength < 1 || keyLength > 1048576 {
		vm.NativeError = "core:crypto.evpKDF invalid hasher, iteration count, or key length"
		return vm.Null
	}
	passwordBytes, saltBytes := []byte(password), []byte(salt)
	derived := make([]byte, 0, keyLength)
	var previous []byte
	for len(derived) < keyLength {
		state := factory()
		_, _ = state.Write(previous)
		_, _ = state.Write(passwordBytes)
		_, _ = state.Write(saltBytes)
		previous = state.Sum(nil)
		for round := 1; round < iterations; round++ {
			state = factory()
			_, _ = state.Write(previous)
			previous = state.Sum(nil)
		}
		derived = append(derived, previous...)
	}
	return cryptoBytesValue(derived[:keyLength])
}

func cryptoKDFCompat(native func(*VM, []OSValue) OSValue) func(*VM, []OSValue) OSValue {
	return func(vm *VM, args []OSValue) OSValue {
		if len(args) < 2 || len(args) > 3 {
			vm.NativeError = "KDF expects password, salt, and optional config"
			return vm.Null
		}
		keySize, iterations, name := 4, 1, "SHA1"
		if len(args) == 3 && args[2].Type == OSVObject && args[2].Opaque != nil {
			config := (*Object)(args[2].Opaque)
			keySize = cryptoConfigNumber(args[2], "keySize", keySize)
			iterations = cryptoConfigNumber(args[2], "iterations", iterations)
			if hasherValue, exists := config.Values["hasher"]; exists && hasherValue.Type == OSVObject && hasherValue.Opaque != nil {
				if hasher, ok := (*Object)(hasherValue.Opaque).Native.(cryptoHasherConfig); ok {
					name = hasher.name
				}
			}
		}
		if keySize < 1 || keySize > 262144 {
			vm.NativeError = "KDF keySize is out of range"
			return vm.Null
		}
		if _, _, exists := cryptoHashFactory(name, 0); !exists {
			vm.NativeError = "KDF hasher is unsupported"
			return vm.Null
		}
		return native(vm, []OSValue{
			args[0], args[1], {Type: OSVNum, Number: float64(iterations)},
			{Type: OSVNum, Number: float64(keySize * 4)}, cryptoString(name),
		})
	}
}

func nativeCryptoParse(vm *VM, args []OSValue) OSValue {
	encoding, _ := cryptoText(args[0])
	return cryptoParse(vm, encoding, args[1])
}

func cryptoParse(vm *VM, encoding string, value OSValue) OSValue {
	text, ok := cryptoText(value)
	if !ok {
		vm.NativeError = "core:crypto.parse expects a string"
		return vm.Null
	}
	var result []byte
	var err error
	switch strings.ToLower(encoding) {
	case "hex":
		result, err = hex.DecodeString(text)
	case "base64":
		result, err = base64.StdEncoding.DecodeString(text)
	case "base64url":
		result, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(text, "="))
	case "utf8":
		if !utf8.ValidString(text) {
			err = errors.New("invalid UTF-8 string")
		} else {
			result = []byte(text)
		}
	case "latin1":
		result = make([]byte, 0, len(text))
		for _, r := range text {
			if r > 255 {
				err = errors.New("Latin1 input contains a code point above 255")
				break
			}
			result = append(result, byte(r))
		}
	case "utf16be", "utf16":
		result = utf16Bytes(text, false)
	case "utf16le":
		result = utf16Bytes(text, true)
	default:
		err = errors.New("unknown encoding")
	}
	if err != nil {
		vm.NativeError = "core:crypto.parse: " + err.Error()
		return vm.Null
	}
	return cryptoBytesValue(result)
}

func utf16Bytes(value string, littleEndian bool) []byte {
	units := utf16.Encode([]rune(value))
	result := make([]byte, len(units)*2)
	for index, unit := range units {
		if littleEndian {
			result[index*2] = byte(unit)
			result[index*2+1] = byte(unit >> 8)
		} else {
			result[index*2] = byte(unit >> 8)
			result[index*2+1] = byte(unit)
		}
	}
	return result
}

func nativeCryptoStringify(vm *VM, args []OSValue) OSValue {
	encoding, _ := cryptoText(args[0])
	bytes, ok := cryptoWordArrayBytes(args[1])
	if !ok {
		vm.NativeError = "core:crypto.stringify expects an encoding and WordArray"
		return vm.Null
	}
	var result string
	switch strings.ToLower(encoding) {
	case "hex":
		result = hex.EncodeToString(bytes)
	case "base64":
		result = base64.StdEncoding.EncodeToString(bytes)
	case "base64url":
		result = base64.RawURLEncoding.EncodeToString(bytes)
	case "utf8":
		if !utf8.Valid(bytes) {
			vm.NativeError = "core:crypto.stringify: invalid UTF-8 data"
			return vm.Null
		}
		result = string(bytes)
	case "latin1":
		runes := make([]rune, len(bytes))
		for index, value := range bytes {
			runes[index] = rune(value)
		}
		result = string(runes)
	case "utf16be", "utf16":
		result = decodeUTF16(bytes, false)
	case "utf16le":
		result = decodeUTF16(bytes, true)
	default:
		vm.NativeError = "core:crypto.stringify: unknown encoding"
		return vm.Null
	}
	return cryptoString(result)
}

func cryptoDigestExport(name string, defaultOutputLength int) OSValue {
	return cryptoFunction(-1, func(vm *VM, args []OSValue) OSValue {
		if len(args) < 1 || len(args) > 2 {
			vm.NativeError = "hash function expects a message and optional config"
			return vm.Null
		}
		message, ok := cryptoBytesFromValue(args[0])
		if !ok {
			vm.NativeError = "hash function expects a string or WordArray message"
			return vm.Null
		}
		outputLength := defaultOutputLength
		if len(args) == 2 {
			outputLength = cryptoConfigNumber(args[1], "outputLength", outputLength)
		}
		factory, _, exists := cryptoHashFactory(name, outputLength)
		if !exists {
			vm.NativeError = "unsupported hash output length"
			return vm.Null
		}
		state := factory()
		_, _ = state.Write(message)
		return cryptoBytesValue(state.Sum(nil))
	})
}

func cryptoHMACExport(name string, defaultOutputLength int) OSValue {
	return cryptoFunction(-1, func(vm *VM, args []OSValue) OSValue {
		if len(args) < 2 || len(args) > 3 {
			vm.NativeError = "HMAC function expects message, key, and optional config"
			return vm.Null
		}
		message, messageOK := cryptoBytesFromValue(args[0])
		key, keyOK := cryptoBytesFromValue(args[1])
		if !messageOK || !keyOK {
			vm.NativeError = "HMAC expects string or WordArray message and key"
			return vm.Null
		}
		outputLength := defaultOutputLength
		if len(args) == 3 {
			outputLength = cryptoConfigNumber(args[2], "outputLength", outputLength)
		}
		factory, _, exists := cryptoHashFactory(name, outputLength)
		if !exists {
			vm.NativeError = "unsupported HMAC output length"
			return vm.Null
		}
		state := hmac.New(factory, key)
		_, _ = state.Write(message)
		return cryptoBytesValue(state.Sum(nil))
	})
}

func cryptoHasherObject(name string, outputLength int) OSValue {
	return cryptoObject(map[string]OSValue{
		"create": cryptoFunction(-1, nativeCryptoHasherCreate(cryptoHasherConfig{name: name, outputLength: outputLength})),
	}, cryptoHasherConfig{name: name, outputLength: outputLength})
}

func cryptoEncoderObject(name string) OSValue {
	if cryptoEncoderClass == nil {
		cryptoEncoderClass = &Class{Name: "Encoder", Template: map[string]OSValue{
			"parse":     nativeMethodTemplate(1, cryptoEncoderParse),
			"stringify": nativeMethodTemplate(1, cryptoEncoderStringify),
		}}
	}
	return OSValue{Type: OSVObject, Opaque: unsafe.Pointer(&Object{
		ClassName: "Encoder", Class: cryptoEncoderClass,
		Native: &cryptoEncoder{name: name}, Values: map[string]OSValue{},
	})}
}

func getCoreCrypto() map[string]OSValue {
	wordArray := cryptoObject(map[string]OSValue{
		"create": cryptoFunction(-1, nativeCryptoWordArray),
		"random": cryptoFunction(1, nativeCryptoWordArrayRandom),
	}, nil)
	encoders := map[string]OSValue{}
	for _, entry := range [][2]string{{"Hex", "hex"}, {"Latin1", "latin1"}, {"Utf8", "utf8"}, {"Utf16", "utf16be"}, {"Utf16BE", "utf16be"}, {"Utf16LE", "utf16le"}, {"Base64", "base64"}, {"Base64url", "base64url"}} {
		encoders[entry[0]] = cryptoEncoderObject(entry[1])
	}
	enc := cryptoObject(encoders, nil)

	hashNames := []string{"MD5", "SHA1", "SHA224", "SHA256", "SHA384", "SHA512", "SHA3", "RIPEMD160"}
	hashExports := make(map[string]OSValue, len(hashNames))
	macExports := make(map[string]OSValue, len(hashNames))
	algoExports := make(map[string]OSValue, len(hashNames)+2)
	cryptoJSValues := map[string]OSValue{
		"lib": cryptoObject(map[string]OSValue{"WordArray": wordArray}, nil),
		"enc": enc,
	}
	for _, name := range hashNames {
		outputLength := 0
		if name == "SHA3" {
			outputLength = 512
		}
		digest := cryptoDigestExport(name, outputLength)
		mac := cryptoHMACExport(name, outputLength)
		hashExports[name] = digest
		macExports["Hmac"+name] = mac
		algoExports[name] = cryptoHasherObject(name, outputLength)
		cryptoJSValues[name] = digest
		cryptoJSValues["Hmac"+name] = mac
	}
	cryptoJSValues["HmacSHA1"] = macExports["HmacSHA1"]
	cryptoJSValues["HmacSHA224"] = macExports["HmacSHA224"]
	cryptoJSValues["HmacSHA256"] = macExports["HmacSHA256"]
	cryptoJSValues["HmacSHA384"] = macExports["HmacSHA384"]
	cryptoJSValues["HmacSHA512"] = macExports["HmacSHA512"]
	cryptoJSValues["HmacSHA3"] = macExports["HmacSHA3"]
	cryptoJSValues["HmacRIPEMD160"] = macExports["HmacRIPEMD160"]
	cryptoJSValues["HmacMD5"] = macExports["HmacMD5"]
	cryptoJSValues["PBKDF2"] = cryptoFunction(-1, cryptoKDFCompat(nativeCryptoPBKDF2))
	cryptoJSValues["EvpKDF"] = cryptoFunction(-1, cryptoKDFCompat(nativeCryptoEvpKDF))
	cryptoJSValues["algo"] = cryptoObject(algoExports, nil)
	cryptoJS := cryptoObject(cryptoJSValues, nil)

	result := map[string]OSValue{
		"digest": cryptoFunction(-1, nativeCryptoDigest), "hmac": cryptoFunction(-1, nativeCryptoHMAC),
		"pbkdf2": cryptoFunction(5, nativeCryptoPBKDF2), "evpKDF": cryptoFunction(5, nativeCryptoEvpKDF),
		"parse": cryptoFunction(2, nativeCryptoParse), "stringify": cryptoFunction(2, nativeCryptoStringify),
		"WordArray": wordArray, "CryptoJS": cryptoJS, "enc": enc, "algo": cryptoJSValues["algo"],
		"PBKDF2": cryptoJSValues["PBKDF2"], "EvpKDF": cryptoJSValues["EvpKDF"],
	}
	for name, value := range hashExports {
		result[name] = value
	}
	for name, value := range macExports {
		result[name] = value
	}
	return result
}

func decodeUTF16(bytes []byte, littleEndian bool) string {
	units := make([]uint16, 0, len(bytes)/2)
	for index := 0; index+1 < len(bytes); index += 2 {
		if littleEndian {
			units = append(units, uint16(bytes[index])|uint16(bytes[index+1])<<8)
		} else {
			units = append(units, uint16(bytes[index])<<8|uint16(bytes[index+1]))
		}
	}
	return string(utf16.Decode(units))
}

func nativeCryptoWordArray(vm *VM, args []OSValue) OSValue {
	if len(args) == 0 {
		return cryptoBytesValue(nil)
	}
	if len(args) >= 1 && len(args) <= 2 {
		if bytes, ok := cryptoWordArrayBytes(args[0]); ok {
			return cryptoBytesValue(bytes)
		}
		if args[0].Type == OSVArray && args[0].Opaque != nil {
			words := (*Array)(args[0].Opaque).Elements
			bytes := make([]byte, len(words)*4)
			for index, value := range words {
				if !IsNumber(value) {
					vm.NativeError = "core:crypto.WordArray.create expects numeric words"
					return vm.Null
				}
				word := uint32(int64(value.Number))
				bytes[index*4] = byte(word >> 24)
				bytes[index*4+1] = byte(word >> 16)
				bytes[index*4+2] = byte(word >> 8)
				bytes[index*4+3] = byte(word)
			}
			sigBytes := len(bytes)
			if len(args) == 2 {
				if !IsNumber(args[1]) || args[1].Number < 0 || args[1].Number > float64(len(bytes)) {
					vm.NativeError = "core:crypto.WordArray.create sigBytes is out of range"
					return vm.Null
				}
				sigBytes = int(args[1].Number)
			}
			return cryptoBytesValue(bytes[:sigBytes])
		}
	}
	vm.NativeError = "core:crypto.WordArray.create expects words and optional sigBytes"
	return vm.Null
}

func nativeCryptoWordArrayRandom(vm *VM, args []OSValue) OSValue {
	if !IsNumber(args[0]) {
		vm.NativeError = "core:crypto.WordArray.random expects a byte length"
		return vm.Null
	}
	length := int(args[0].Number)
	if length < 0 || length > 1048576 {
		vm.NativeError = "core:crypto.WordArray.random byte length is out of range"
		return vm.Null
	}
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		vm.NativeError = "core:crypto.WordArray.random: " + err.Error()
		return vm.Null
	}
	return cryptoBytesValue(bytes)
}
