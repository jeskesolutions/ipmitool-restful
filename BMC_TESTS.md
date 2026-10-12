# BMC testing

At the moment, this project has only been tested on **iLO 4** and **iLO 5**.

| BMC | Test status |
| --- | --- |
| iLO 4 | Tested |
| iLO 5 | Tested |
| Supermicro | Pending |
| Huawei | Pending |
| iDRAC | Pending |

## WIP

Planned work:

- [ ] Add SQLite support to store BMC connection details (IP or hostname, username,
  password, and connection options), generate a local ID for each saved BMC, and
  reuse that ID in subsequent requests without resending the connection data.
