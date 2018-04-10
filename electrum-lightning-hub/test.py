from repeater_and_rpc import make_h2handler, mkhandler, Queues
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
coro = loop.create_server(make_h2handler(8433, killQueuePort=8432, assoc={8432: queues}), '127.0.0.1', 9090)
elec1 = loop.create_server(mkhandler(8432), '127.0.0.1', 8433)

async def myfun():
    try:
        await coro
        await elec1

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

asyncio.ensure_future(queueMonitor(queues.readQueue, queues.writeQueue, 8432, queues.killQueue))
asyncio.ensure_future(myfun())
asyncio.ensure_future(myfun2())

loop.run_forever()
