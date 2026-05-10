import http from 'k6/http';
import { check, sleep } from 'k6';

export const options = {
  stages: [
    { duration: '5s', target: 50 },  // Ramp up to 50 concurrent users
    { duration: '10s', target: 50 },   // Hold at 50 users
    { duration: '5s', target: 200 }, // Spike to 200 users to test circuit breaker & rate limit
    { duration: '5s', target: 0 },   // Ramp down
  ],
  thresholds: {
    http_req_duration: ['p(95)<500'], // 95% of requests must complete below 500ms
  },
};

export default function () {
  // 1. Test GET request to trigger the Round-Robin / Least Connections balancers
  const getParams = {
    headers: {
      'Authorization': 'Bearer secret123',
    },
  };
  const resUsers = http.get('http://localhost:8080/users', getParams);
  check(resUsers, {
    'status is 200 or 429': (r) => r.status === 200 || r.status === 429,
  });

  // 2. Test Idempotent POST request to trigger the Idempotency Middleware
  // Each Virtual User (__VU) will use the same idempotency key for all iterations,
  // meaning the 1st request hits the upstream, and subsequent requests hit the Redis cache!
  const payload = JSON.stringify({ amount: 100, currency: "USD" });
  const params = {
    headers: {
      'Content-Type': 'application/json',
      'Authorization': 'Bearer secret123',
      'Idempotency-Key': `k6-user-${__VU}`, 
    },
  };
  
  const resPayments = http.post('http://localhost:8080/payments', payload, params);
  check(resPayments, {
    'payments status 200/201 or 429': (r) => [200, 201, 429].includes(r.status),
  });

  // Sleep slightly to prevent complete client-side CPU saturation
  sleep(0.1);
}
