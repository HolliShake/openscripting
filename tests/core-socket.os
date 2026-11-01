import { socket, AF_INET, SOCK_STREAM } from "core:socket";
import { println } from "core:io";

const unused = socket(AF_INET, SOCK_STREAM);
unused->close();

const listener = socket(AF_INET, SOCK_STREAM);
listener->bind(["127.0.0.1", 0]);
listener->listen(5);
const accepted = listener->accept();
const address = listener->getsockname();

var serve::fn() async {
	const peerInfo = await accepted;
	const peer = peerInfo[0];
	const request = await peer->recv(4);
	if (request != "ping") {
		println("FAIL: socket server read", request);
	}
	await peer->send("pong");
	peer->close();
	listener->close();
};

var connect::fn() async {
	const client = socket(AF_INET, SOCK_STREAM);
	client->connect(address);
	await client->sendall("ping");
	const response = await client->recv(4);
	if (response != "pong") {
		println("FAIL: socket client read", response);
	}
	client->close();
};

serve();
connect();
println("core socket ok");
