import json
import asyncio
from aiohttp import web
import subprocess
import shlex
import base64

def make_app(realPortsSupplier):
    async def handler(request):
        content = await request.content.read()
        parsed_request = json.loads(content.decode("ascii"))
        portPair = await realPortsSupplier.get(base64.b64decode(parsed_request["params"][0]))
        proc = await asyncio.create_subprocess_shell(cmd="~/go/bin/lncli --macaroonpath=" + portPair.datadir + "/admin.macaroon --rpcserver=localhost:" + str(portPair.lndRPCPort) + " " + shlex.quote(parsed_request["method"]) + " " + " ".join(shlex.quote(x) for x in parsed_request["params"][1:]), stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        response = {}
        try:
          await asyncio.wait_for(proc.wait(), 15)
        except asyncio.TimeoutError:
          response = {"result": "timeout in lncli_endpoint"}
        else:  
          response = {"result": {"stdout": (await proc.stdout.read()).decode("ascii"),
                                 "stderr": (await proc.stderr.read()).decode("ascii"),
                                 "returncode": proc.returncode}}
        return web.Response(body=json.dumps(response))
    app = web.Application()
    app.router.add_route('POST', "", handler)
    return app

def create_on_loop(loop, realPortsSupplier):
    return loop.create_server(make_app(realPortsSupplier).make_handler(), '0.0.0.0', 8090)

if __name__ == "__main__":
    loop = asyncio.get_event_loop()
    loop.run_until_complete(create_on_loop(loop))
    loop.run_forever()
