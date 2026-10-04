// 协议报文定义。
//
// 三次握手交换双方的“代次号”(generation) 与本次握手随机数 (nonce)：
//   RESET       我方新代次是 gen，本次握手 nonce
//   RESET_ACK   我方代次是 gen/nonce，且已确认你方的 (ackGen, ackNonce)
//   CONFIRM     确认你方的 (ackGen, ackNonce)，握手完成
// 数据报文携带发送方代次 gen 与从 0 重新计数的序号 seq。

export interface ResetMsg {
  kind: 'RESET';
  gen: number;
  nonce: string;
}

export interface ResetAckMsg {
  kind: 'RESET_ACK';
  gen: number;
  nonce: string;
  ackGen: number;
  ackNonce: string;
}

export interface ConfirmMsg {
  kind: 'CONFIRM';
  gen: number;
  nonce: string;
  ackGen: number;
  ackNonce: string;
}

export interface DataMsg {
  kind: 'DATA';
  gen: number;
  seq: number;
  payload: string;
}

export type Message = ResetMsg | ResetAckMsg | ConfirmMsg | DataMsg;

export function fmtMsg(m: Message): string {
  switch (m.kind) {
    case 'RESET':
      return `RESET(gen=${m.gen}, nonce=${m.nonce})`;
    case 'RESET_ACK':
      return `RESET_ACK(gen=${m.gen}, nonce=${m.nonce}, ack=${m.ackGen}/${m.ackNonce})`;
    case 'CONFIRM':
      return `CONFIRM(gen=${m.gen}, ack=${m.ackGen}/${m.ackNonce})`;
    case 'DATA':
      return `DATA(gen=${m.gen}, seq=${m.seq}, "${m.payload}")`;
  }
}
