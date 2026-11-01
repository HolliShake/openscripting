import { argv } from "core:sys";
import { println } from "core:io";

if (argv[0] != "first argument" || argv[1] != "second") {
	println("FAIL: sys.argv contents or order", argv);
}

println("core sys argv ok");