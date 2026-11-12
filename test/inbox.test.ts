import assert from 'node:assert/strict';
import { describe, it } from 'node:test';
import { createInboxAuthorizer } from '../src/index.ts';
import type {
  FetchOutcome,
  InboxActivity,
  InboxRequest,
  InboxSignatureContext,
} from '../src/index.ts';

const ALICE = 'https://alice.example/actors/alice';
const ALICE_KEY = `${ALICE}#main-key`;

const request: InboxRequest = {
  method: 'POST',
  url: 'https://receiver.example/inbox',
  headers: { signature: `keyId="${ALICE_KEY}"` },
};

describe('createInboxAuthorizer 收件箱装配', () => {
  it('签名校验通过后委托给授权内核', async () => {
    const verifier = async (): Promise<InboxSignatureContext> => ({ keyId: ALICE_KEY });
    const fetcher = async (objectId: string): Promise<FetchOutcome> => ({
      id: objectId,
      attributedTo: ALICE,
    });
    const authorizer = createInboxAuthorizer({
      verifySignature: verifier,
      fetchObject: fetcher,
    });

    const activity: InboxActivity = {
      type: 'Follow',
      actor: ALICE,
      object: 'https://bob.example/actors/bob',
    };

    const result = await authorizer(request, activity);
    assert.equal(result.allowed, true);
    assert.equal(result.actorId, ALICE);
  });

  it('签名校验抛错时返回 signature_rejected 且不取回对象', async () => {
    const verifier = async (): Promise<InboxSignatureContext> => {
      throw new Error('digest mismatch');
    };
    let fetchCalls = 0;
    const fetcher = async (): Promise<FetchOutcome> => {
      fetchCalls += 1;
      return { id: 'https://bob.example/note', attributedTo: ALICE };
    };
    const authorizer = createInboxAuthorizer({
      verifySignature: verifier,
      fetchObject: fetcher,
    });

    const activity: InboxActivity = {
      type: 'Create',
      actor: ALICE,
      object: { id: 'https://bob.example/note', attributedTo: ALICE },
    };

    const result = await authorizer(request, activity);
    assert.equal(result.allowed, false);
    assert.equal(result.reason, 'signature_rejected');
    assert.equal(fetchCalls, 0);
  });

  it('签名合法但活动 actor 被冒用仍被内核拒绝', async () => {
    const verifier = async (): Promise<InboxSignatureContext> => ({ keyId: ALICE_KEY });
    const fetcher = async (objectId: string): Promise<FetchOutcome> => ({
      id: objectId,
      attributedTo: ALICE,
    });
    const authorizer = createInboxAuthorizer({
      verifySignature: verifier,
      fetchObject: fetcher,
    });

    const activity: InboxActivity = {
      type: 'Follow',
      actor: 'https://mallory.example/actors/mallory',
      object: 'https://bob.example/actors/bob',
    };

    const result = await authorizer(request, activity);
    assert.equal(result.allowed, false);
    assert.equal(result.reason, 'signer_actor_mismatch');
  });
});
