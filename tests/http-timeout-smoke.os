import { Express, fetch } from "core:http";
import { socket, AF_INET, SOCK_STREAM } from "core:socket";
import { println } from "core:io";

const app = Express();
app->set("handler timeout", 30);
var address = "";
const pendingListener = socket(AF_INET, SOCK_STREAM);
pendingListener->bind(["127.0.0.1", 0]);
pendingListener->listen(1);
const pendingAccept = pendingListener->accept();

app->get("/wait", fn(req, res) async {
	await pendingAccept;
	res->send("handler resumed");
});

address = app->listen("127.0.0.1:0");

var checkTimeout = fn() async {
	let response = await fetch("http://" + address + "/wait");
	println("handler timeout:", response->status);
	if (response->status == 504) {
		println("PASS: pending handler timed out");
	} else {
		println("FAIL: expected 504");
	}
	app->close();
	pendingListener->close();
};

checkTimeout();