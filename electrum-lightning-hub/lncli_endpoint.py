import json
import asyncio
from aiohttp import web
import subprocess
import shlex
import base64
import logging

def make_app(realPortsSupplier):
    async def handleGet(request):
        portPair = await realPortsSupplier.get(base64.b64decode(request.query["privKeyHash"]))
        proc = await asyncio.create_subprocess_shell(cmd="tar c " + shlex.quote(portPair.datadir), stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
          await asyncio.wait_for(proc.wait(), 5)
        except asyncio.TimeoutError:
          return web.Response(status=500)
        tar = await proc.stdout.read()
        return web.Response(body=tar)
    async def handlePost(request):
        content = await request.content.read()
        parsed_request = json.loads(content.decode("ascii"))
        portPair = await realPortsSupplier.get(base64.b64decode(parsed_request["params"][0]))
        #--macaroonpath=" + portPair.datadir + "/admin.macaroon
        cmd = "~/go/bin/lncli --no-macaroons --rpcserver=localhost:" + str(portPair.lndRPCPort) + " " + shlex.quote(parsed_request["method"]) + " " + " ".join(shlex.quote(x) for x in parsed_request["params"][1:])
        logging.info(cmd)
        proc = await asyncio.create_subprocess_shell(cmd=cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        response = {}
        try:
          await asyncio.wait_for(proc.wait(), 300)
        except asyncio.TimeoutError:
          response = {"result": "timeout in lncli_endpoint"}
        else:  
          response = {"result": {"stdout": (await proc.stdout.read()).decode("ascii"),
                                 "stderr": (await proc.stderr.read()).decode("ascii"),
                                 "returncode": proc.returncode}}
        return web.json_response(response)
    app = web.Application()
    app.router.add_route('POST', "", handlePost)
    app.router.add_route('GET', "", handleGet)
    return app

def create_on_loop(loop, realPortsSupplier):
    return loop.create_server(make_app(realPortsSupplier).make_handler(), '0.0.0.0', 8090)

if __name__ == "__main__":
    loop = asyncio.get_event_loop()
    loop.run_until_complete(create_on_loop(loop))
    loop.run_forever()
