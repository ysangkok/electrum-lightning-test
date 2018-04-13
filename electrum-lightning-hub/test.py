from repeater_and_rpc import make_h2handler, Queues
import asyncio
from lib.ln.electrum_bridge import rpc_pb2_grpc, rpc_pb2
from google.protobuf import json_format
from aiogrpc import insecure_channel
import traceback
from socksserver import queueMonitor
import json

chan = insecure_channel("ipv4:///127.0.0.1:9090")

serv = rpc_pb2_grpc.ElectrumBridgeStub(chan)

loop = asyncio.get_event_loop()

queues = Queues()
coro = loop.create_server(make_h2handler(8432, {8432: queues}), '127.0.0.1', 9090)

async def myfun():
    try:
        await coro

        for i in range(2):
            res = await serv.IsSynced(rpc_pb2.IsSyncedRequest())
            print(res)
        loop.stop()
    except:
        traceback.print_exc()

async def myfun2():
    for i in range(2):
        req = json.loads(await queues.writeQueue.get())
        data = json_format.MessageToJson(rpc_pb2.IsSyncedResponse())
        data = {"id": req["id"], "result": data}
        await queues.readQueue.put(json.dumps(data).encode("ascii"))

loop.create_task(queueMonitor(queues.readQueue, queues.writeQueue, 8432, queues.killQueue))
loop.create_task(myfun())
loop.create_task(myfun2())

loop.run_forever()
