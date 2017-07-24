#!/usr/bin/env python

# this should repeat signing requests from lnd and launch lncli for rpc

import itertools
import traceback
import sys
import asyncio
import websockets
import logging
import os
from enum import Enum

logging.basicConfig(level=logging.INFO)

q = asyncio.PriorityQueue()
to_lnd = asyncio.Queue()

#class State(Enum):
#	need_key = 0
#	need_req = 1
#	need_ack_did_not_send = 2
#	need_ack_did_send = 2
#
#state = State.need_key

#async def handle_conn(conn, path):
#	global state
#	if state == State.need_key:
#		conn.send(b"key not received")
#		return
#	conn.send(key)
#	while True:
#		print(state)
#		if state == State.need_req:
#			await asyncio.sleep(1)
#			if q.qsize() > 0:
#				state = State.need_ack_did_not_send
#		elif state == State.need_ack_did_not_send:
#			prio, msg = await q.get()
#			await q.put((prio, msg))
#			conn.send(msg)
#			state = State.need_ack_did_send
#		elif state == State.need_ack_did_send:
#			repl = await conn.recv()
#			await to_lnd.put(repl)
#			state = State.need_req

async def handle_conn(conn, path):
		global q
		global to_lnd
		global key
		sent_idx = -1
		try:
				assert key is not None
				await conn.send(key)
				ack_needed = False
				while True:
						ack_needed = q.qsize() > 0
						if ack_needed:
								prio, msg = await q.get()
								if sent_idx < prio:
									await conn.send(msg)
									sent_idx = prio
								await q.put((prio, msg))
						recv_task = asyncio.ensure_future(conn.recv())
						def mark_recv_done(x):
								nonlocal recv_done
								recv_done = True
						tasks = [recv_task]
						recv_task.add_done_callback(mark_recv_done)
						queue_done = recv_done = False
						if not ack_needed:
								queue_task = asyncio.ensure_future(q.get())
								def mark_queue_done(x):
										global loop
										nonlocal queue_done
										nonlocal sent_idx
										queue_done = True
										if not x.cancelled():
											async def fun():
												await q.put(x.result())
											loop.call_soon(lambda: asyncio.ensure_future(fun()))
								queue_task.add_done_callback(mark_queue_done)
								tasks.append(queue_task)
						try:
								done, pending = await asyncio.wait_for(asyncio.shield(asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)), 1.0)
						except asyncio.TimeoutError:
								for job in tasks:
									job.cancel()
								if ack_needed:
									if q.qsize() == 0:
										await q.put((prio, msg))
								continue
						for task in tasks:
							try:
								ex = task.exception()
								if ex: print(ex)
							except asyncio.futures.InvalidStateError:
								pass
						if not ack_needed and not queue_task.cancelled():
							try:
								r = queue_task.result()
							except asyncio.futures.InvalidStateError:
								break
							if sent_idx < r[0]:
								await conn.send(r[1])
								sent_idx = r[0]

						doneone = next(iter(done))
						if queue_done and recv_done:
								res = await asyncio.gather(*tasks, return_exceptions=True)
								gotex = any(isinstance(x, websockets.protocol.ConnectionClosed) for x in res)
								if ack_needed:
									if gotex:
										await q.put((prio, msg))
										break
						for p in pending: p.cancel()
						if recv_done:
								if recv_task.exception():
										print("BREAKING")
										break
								if recv_task.result().strip() == b"":
										print("ignoring newline")
								else:
										assert q.qsize() > 0
										print("dumped", await q.get())
										await to_lnd.put(recv_task.result())
		except Exception:
				traceback.print_exc()
				globtask.cancel()

key = None

async def client(hostport):
		global globtask, key, state
		while True:
			try:
					async with websockets.connect('ws://' + hostport) as lnd:
						key = await lnd.recv()
						#if state == State.need_key: state = State.need_req
						for idx in itertools.count():
								async def f1():
										req = await lnd.recv()
										print("< {}".format(req))
										await q.put((idx, req))
								async def f2():
										rply = await to_lnd.get()
										print("> {}".format(rply))
										await lnd.send(rply)
								l = [asyncio.ensure_future(x()) for x in [f1, f2]]
								try:
									done, pending = await asyncio.wait_for(asyncio.shield(asyncio.wait(l, return_when=asyncio.FIRST_COMPLETED)), 1.0)
								except asyncio.TimeoutError:
									for job in l: job.cancel()
									continue
								for p in pending: p.cancel()
								doneone = next(iter(done))
								if doneone.exception():
										raise doneone.exception()
						await asyncio.sleep(1)
			except Exception as e:
				print(e)
				await asyncio.sleep(5)
				#globtask.cancel()

#async def status():
#		while True:
#				print("q: {} to_lnd: {}".format(q.qsize(), to_lnd.qsize()))
#				await asyncio.sleep(5)

server = websockets.serve(handle_conn, '0.0.0.0', 8765)

loop = asyncio.get_event_loop()
loop.set_debug(False)
l = [server, client(os.environ["ELECTRUM_LND_HOST_PORT"])]
globtask = asyncio.ensure_future(asyncio.wait(l))
loop.run_until_complete(globtask)
loop.run_forever()
loop.close()
