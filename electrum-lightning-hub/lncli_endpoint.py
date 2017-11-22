import json
import asyncio
from aiohttp import web
import subprocess
import shlex

async def handler(request):
    content = await request.content.read()
    parsed_request = json.loads(content.decode("ascii"))
    proc = await asyncio.create_subprocess_shell(cmd="~/go/bin/lncli " + parsed_request["method"] + " " + " ".join(shlex.quote(x) for x in parsed_request["params"]), stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    await proc.wait()
    response = {"result": {"stdout": (await proc.stdout.read()).decode("ascii"),
                           "stderr": (await proc.stderr.read()).decode("ascii"),
                           "returncode": proc.returncode}}
    return web.Response(body=json.dumps(response))

def make_app():
    app = web.Application()
    app.router.add_route('POST', "", handler)
    return app

def create_on_loop(loop):
    return loop.create_server(make_app().make_handler(), '0.0.0.0', 8090)

if __name__ == "__main__":
    loop = asyncio.get_event_loop()
    loop.run_until_complete(create_on_loop(loop))
    loop.run_forever()
