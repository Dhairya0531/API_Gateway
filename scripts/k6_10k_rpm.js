import http from 'k6/http';
import { check } from 'k6';

export const options = {
  scenarios: {
    constant: {
      executor: 'constant-arrival-rate',
      rate: 167,            // ~10,020 requests/min
      timeUnit: '1s',
      duration: '2m',
      preAllocatedVUs: 300,
      maxVUs: 800,
    },
  },
};

export default function () {
  const params = {
    headers: {
      'Authorization': 'Bearer secret123',
    },
  };

  const res = http.get('http://localhost:8080/users', params);
  check(res, { 'status 200 or 429': (r) => r.status === 200 || r.status === 429 });
}
